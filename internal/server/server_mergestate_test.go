package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// T48 合并状态查询端点测试(GET /api/v1/merge-state,调研 §1.3/§1.4):
// idle 200(轮询稳态,逐字段含 null/[]/两布尔)/ merging 200(字段逐项)/
// 项目 404 / 分支 404 / 空白参数 400 / 非 GET 405。全程临时 SQLite 库
// (参照 internal/testdb;设置 KB_TEST_DSN 时随 freshDSN 走 PostgreSQL 回归)。

// idleBody 是 idle 稳态的完整 JSON(精确匹配钉死字段序、null 事实字段、
// 空数组 conflicts 与两布尔 false)。
const idleBody = `{
  "project": "default",
  "branch": "main",
  "state": "idle",
  "can_continue": false,
  "can_abort": false,
  "base": null,
  "theirs": null,
  "ours": null,
  "conflicts": [],
  "conflict_count": 0,
  "merged_branch": null
}
`

// assertMergeStateKeyOrder 断言响应字段按契约顺序出现(view.MergeStateRow 的
// 字段序即契约:两出口一份实现,CLI parity 复用同一结构体)。
func assertMergeStateKeyOrder(t *testing.T, body string) {
	t.Helper()
	keys := []string{"\"project\"", "\"branch\"", "\"state\"", "\"can_continue\"", "\"can_abort\"", "\"base\"", "\"theirs\"", "\"ours\"", "\"conflicts\"", "\"conflict_count\"", "\"merged_branch\""}
	last := -1
	for _, k := range keys {
		i := strings.Index(body, k)
		if i < 0 {
			t.Fatalf("merge-state 响应缺字段 %s: %s", k, body)
		}
		if i < last {
			t.Fatalf("merge-state 字段序破坏:%s 应在 %d 之后: %s", k, last, body)
		}
		last = i
	}
}

func TestServeMergeStateIdle(t *testing.T) {
	ts := newTestServer(t)
	res, body := do(t, ts, http.MethodGet, "/api/v1/merge-state")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("idle 应 200(轮询稳态不是错误),得到 %d:%s", res.StatusCode, body)
	}
	if got := string(body); got != idleBody {
		t.Fatalf("idle 响应应逐字节符合契约,得到:\n%s", got)
	}
	assertMergeStateKeyOrder(t, string(body))
	var row view.MergeStateRow
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("idle 解析: %v:%s", err, body)
	}
	if row.Project != "default" || row.Branch != "main" {
		t.Fatalf("idle 应回显作用域: %+v", row)
	}
	if row.State != "idle" {
		t.Fatalf("state 应为 idle: %+v", row)
	}
	if row.CanContinue || row.CanAbort {
		t.Fatalf("idle 两布尔应为 false: %+v", row)
	}
	if row.Base != nil || row.Theirs != nil || row.Ours != nil || row.MergedBranch != nil {
		t.Fatalf("idle 事实字段(含 merged_branch)应为 null: %+v", row)
	}
	if len(row.Conflicts) != 0 || row.ConflictCount != 0 {
		t.Fatalf("idle conflicts 应为空数组且计数 0: %+v", row)
	}
	// 显式参数与省略同结果(作用域回显)
	var explicit view.MergeStateRow
	getJSON(t, ts, "/api/v1/merge-state?project=default&branch=main", &explicit)
	if !reflect.DeepEqual(explicit, row) {
		t.Fatalf("显式参数应与省略同结果: %+v vs %+v", explicit, row)
	}
}

func TestServeMergeStateMerging(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := freshDSN(t), freshDSN(t)
	sA, err := store.Open(ctx, dsnA)
	if err != nil {
		t.Fatalf("打开 A 库: %v", err)
	}
	sB, err := store.Open(ctx, dsnB)
	if err != nil {
		t.Fatalf("打开 B 库: %v", err)
	}
	t.Cleanup(func() { sA.Close(); sB.Close() })
	rA := repo.Open(sA, repo.Config{})
	rB := repo.Open(sB, repo.Config{})
	// 共同基点:task=v1,B fast-forward 拉平;随后双侧异改制造内容冲突
	if _, _, err := rA.SetNote(ctx, "task", repo.NoteInput{Title: "task", Body: "v1"}, "seed"); err != nil {
		t.Fatalf("种入基点: %v", err)
	}
	if _, err := rB.Pull(ctx, sA, "default", "main", false); err != nil {
		t.Fatalf("B 拉平 A: %v", err)
	}
	if _, _, err := rA.SetNote(ctx, "task", repo.NoteInput{Title: "task", Body: "va"}, "va"); err != nil {
		t.Fatalf("A 侧异改: %v", err)
	}
	if _, _, err := rB.SetNote(ctx, "task", repo.NoteInput{Title: "task", Body: "vb"}, "vb"); err != nil {
		t.Fatalf("B 侧异改: %v", err)
	}
	_, err = rA.MergeStart(ctx, sB, "default", "main", repo.MergeOptions{})
	var mc *repo.ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("应检出冲突并建中间态,得到 %v", err)
	}
	srv, err := New(ctx, Options{DSN: dsnA})
	if err != nil {
		t.Fatalf("构造 server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	res, body := do(t, ts, http.MethodGet, "/api/v1/merge-state")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("merging 应 200,得到 %d:%s", res.StatusCode, body)
	}
	assertMergeStateKeyOrder(t, string(body))
	var row view.MergeStateRow
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("merging 解析: %v:%s", err, body)
	}
	// 字段逐项断言(契约字段名一字不改)
	if row.Project != "default" || row.Branch != "main" {
		t.Fatalf("应回显作用域: %+v", row)
	}
	if row.State != "merging" {
		t.Fatalf("state 应为 merging: %+v", row)
	}
	if !row.CanContinue || !row.CanAbort {
		t.Fatalf("merging 两布尔应为 true: %+v", row)
	}
	for name, p := range map[string]*string{"base": row.Base, "theirs": row.Theirs, "ours": row.Ours} {
		if p == nil {
			t.Fatalf("merging 态 %s 不应为 null: %+v", name, row)
		}
		if !strings.HasPrefix(*p, "sha256:") {
			t.Fatalf("merging 态 %s 应为全地址: %q", name, *p)
		}
	}
	if *row.Base == *row.Theirs || *row.Ours == *row.Theirs {
		t.Fatalf("base/theirs/ours 应互不相同: %+v", row)
	}
	if row.MergedBranch == nil || *row.MergedBranch != "main-merge" {
		t.Fatalf("merged_branch 应为 main-merge: %+v", row)
	}
	if len(row.Conflicts) != 1 || row.ConflictCount != 1 {
		t.Fatalf("应恰有 1 条冲突: %+v", row)
	}
	c := row.Conflicts[0]
	if c.Path != "task" {
		t.Fatalf("冲突路径应为 task: %+v", c)
	}
	if c.Kind != "content" {
		t.Fatalf("冲突类别应为 content(双侧异改): %+v", c)
	}
	for name, a := range map[string]hash.Address{"base": c.Base, "ours": c.Ours, "theirs": c.Theirs} {
		if !strings.HasPrefix(string(a), "sha256:") {
			t.Fatalf("冲突 %s 侧应为全地址: %q", name, string(a))
		}
	}
	if c.Ours == c.Theirs {
		t.Fatalf("冲突 ours/theirs 不应相同: %+v", c)
	}
}

func TestServeMergeStateProjectNotFound(t *testing.T) {
	ts := newTestServer(t)
	res, body := do(t, ts, http.MethodGet, "/api/v1/merge-state?project=ghost")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("项目不存在应 404,得到 %d:%s", res.StatusCode, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil || got["error"] == "" {
		t.Fatalf("错误响应应为 {\"error\":…}: %s", body)
	}
	if !strings.Contains(got["error"], "项目") {
		t.Fatalf("错误应指明项目不存在: %s", body)
	}
}

func TestServeMergeStateBranchNotFound(t *testing.T) {
	ts := newTestServer(t)
	res, body := do(t, ts, http.MethodGet, "/api/v1/merge-state?branch=ghost")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("分支不存在应 404,得到 %d:%s", res.StatusCode, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil || got["error"] == "" {
		t.Fatalf("错误响应应为 {\"error\":…}: %s", body)
	}
	if !strings.Contains(got["error"], "分支") {
		t.Fatalf("错误应指明分支不存在: %s", body)
	}
}

func TestServeMergeStateBadRequest(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{
		"/api/v1/merge-state?project=%20",   // 显式空白项目
		"/api/v1/merge-state?branch=%20%20", // 显式空白分支
		"/api/v1/merge-state?project=",      // 显式空值
	} {
		res, body := do(t, ts, http.MethodGet, path)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s 应 400,得到 %d:%s", path, res.StatusCode, body)
		}
		var got map[string]string
		if err := json.Unmarshal(body, &got); err != nil || got["error"] == "" {
			t.Fatalf("%s 错误响应应为 {\"error\":…}: %s", path, body)
		}
	}
}

func TestServeMergeStateMethodNotAllowed(t *testing.T) {
	ts := newTestServer(t)
	res, body := do(t, ts, http.MethodPost, "/api/v1/merge-state")
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("非 GET 应 405,得到 %d:%s", res.StatusCode, body)
	}
	if res.Header.Get("Allow") != "GET" {
		t.Fatalf("405 应带 Allow: GET,得到 %q", res.Header.Get("Allow"))
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil || got["error"] == "" {
		t.Fatalf("错误响应应为 {\"error\":…}: %s", body)
	}
}
