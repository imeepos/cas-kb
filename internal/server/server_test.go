package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// freshDSN 为单个用例创建独立测试库(默认 SQLite;设置 KB_TEST_DSN 时走 PostgreSQL 回归)。
func freshDSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("KB_TEST_DSN") == "" {
		return testdb.NewSQLite(t)
	}
	return testdb.New(t)
}

// newTestServer 派生全新库、种入嵌套笔记,返回就绪的 httptest 服务。
// 种子语料(路径/标题/正文/标签):
//
//	go/concurrency/channel  通道   chan 语义与 select    [go]
//	go/io/reader            读     io reader 语义       [go]
//	hello                   你好   第一条笔记
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := repo.Open(s, repo.Config{})
	for _, in := range []struct {
		path, title, body string
		tags              []string
	}{
		{"go/concurrency/channel", "通道", "chan 语义与 select", []string{"go"}},
		{"go/io/reader", "读", "io reader 语义", []string{"go"}},
		{"hello", "你好", "第一条笔记", nil},
	} {
		if _, _, err := r.SetNote(ctx, in.path, repo.NoteInput{Title: in.title, Body: in.body, Tags: in.tags}, "add "+in.path); err != nil {
			t.Fatalf("种入 %s: %v", in.path, err)
		}
	}
	srv, err := New(ctx, Options{DSN: dsn})
	if err != nil {
		t.Fatalf("构造 server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return ts
}

// firstShort 返回快照链首个(最新)快照的短标识。
func firstShort(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	var rows []map[string]any
	getJSON(t, ts, "/api/v1/log", &rows)
	if len(rows) == 0 {
		t.Fatal("log 应至少有一条")
	}
	id, _ := rows[0]["id"].(string)
	return id
}

// getJSON 断言 200 并解析 JSON。
func getJSON(t *testing.T, ts *httptest.Server, path string, out any) {
	t.Helper()
	res, body := do(t, ts, http.MethodGet, path)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 应 200,得到 %d:%s", path, res.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("GET %s 解析 JSON: %v:%s", path, err, body)
	}
}

// do 发送一次请求,返回响应与响应体。
func do(t *testing.T, ts *httptest.Server, method, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func TestServeHealthz(t *testing.T) {
	ts := newTestServer(t)
	var got map[string]any
	getJSON(t, ts, "/healthz", &got)
	if got["ok"] != true {
		t.Fatalf("ok 应为 true,得到 %v", got)
	}
	backend, _ := got["backend"].(string)
	if backend != "sqlite" && backend != "postgres" {
		t.Fatalf("backend 应为 sqlite|postgres,得到 %q", backend)
	}
	if sv, ok := got["schema_version"].(float64); !ok || int(sv) != store.DBSchemaVersion {
		t.Fatalf("schema_version 应为 %d,得到 %v", store.DBSchemaVersion, got["schema_version"])
	}
	if got["project"] != "default" {
		t.Fatalf("project 应为 default,得到 %v", got["project"])
	}
}

func TestServeProjects(t *testing.T) {
	ts := newTestServer(t)
	var rows []map[string]any
	getJSON(t, ts, "/api/v1/projects", &rows)
	if len(rows) == 0 {
		t.Fatal("至少应有 default 项目")
	}
	for _, key := range []string{"name", "description", "branches"} {
		if _, ok := rows[0][key]; !ok {
			t.Fatalf("projects 行缺字段 %q:%v", key, rows[0])
		}
	}
	if rows[0]["name"] != "default" {
		t.Fatalf("首行应为 default(字典序),得到 %v", rows[0]["name"])
	}
}

// findNode 按路径在嵌套树中定位节点。
func findNode(n map[string]any, path string) map[string]any {
	if n["path"] == path {
		return n
	}
	children, _ := n["children"].([]any)
	for _, c := range children {
		child, _ := c.(map[string]any)
		if got := findNode(child, path); got != nil {
			return got
		}
	}
	return nil
}

func TestServeTree(t *testing.T) {
	ts := newTestServer(t)
	var root map[string]any
	getJSON(t, ts, "/api/v1/tree", &root)
	if root["type"] != "dir" {
		t.Fatalf("根节点应为 dir,得到 %v", root["type"])
	}
	dir := findNode(root, "go/concurrency")
	if dir == nil || dir["type"] != "dir" {
		t.Fatalf("应存在目录节点 go/concurrency:%v", root)
	}
	note := findNode(root, "go/concurrency/channel")
	if note == nil {
		t.Fatalf("应存在嵌套条目节点 go/concurrency/channel:%v", root)
	}
	if note["type"] != "note" || note["title"] != "通道" {
		t.Fatalf("条目节点应带 type=note 与标题:%v", note)
	}
	addr, _ := note["addr"].(string)
	if !strings.HasPrefix(addr, "sha256:") {
		t.Fatalf("条目节点应带 addr(sha256 前缀),得到 %q", addr)
	}
	if _, hasChildren := note["children"]; hasChildren {
		t.Fatal("note 节点不应有 children 字段")
	}

	// at=分支头短标识 与省略 at 同构;at 不存在 → 404
	head := firstShort(t, ts)
	var atRoot map[string]any
	getJSON(t, ts, "/api/v1/tree?at="+head, &atRoot)
	if findNode(atRoot, "go/concurrency/channel") == nil {
		t.Fatalf("at=头快照应看到同样条目:%v", atRoot)
	}
	res, body := do(t, ts, http.MethodGet, "/api/v1/tree?at=deadbeefdeadbeef")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("at 不存在应 404,得到 %d:%s", res.StatusCode, body)
	}
}

func TestServeNote(t *testing.T) {
	ts := newTestServer(t)
	var row map[string]any
	getJSON(t, ts, "/api/v1/note?path=go/concurrency/channel", &row)
	if row["path"] != "go/concurrency/channel" || row["title"] != "通道" {
		t.Fatalf("note 应带 path 与 title:%v", row)
	}
	if row["body"] != "chan 语义与 select" {
		t.Fatalf("note 应带正文原文:%v", row["body"])
	}
	if row["summary"] != "chan 语义与 select" {
		t.Fatalf("note 应带派生摘要:%v", row["summary"])
	}
	tags, _ := row["tags"].([]any)
	if len(tags) != 1 || tags[0] != "go" {
		t.Fatalf("note 应带标签数组:%v", row["tags"])
	}
	// 无标签条目 tags 归一为 [] 而非 null
	var hello map[string]any
	getJSON(t, ts, "/api/v1/note?path=hello", &hello)
	if hello["tags"] == nil {
		t.Fatalf("无标签条目 tags 应为 [],得到 null:%v", hello["tags"])
	}
	if got, ok := hello["tags"].([]any); !ok || len(got) != 0 {
		t.Fatalf("无标签条目 tags 应为 [],得到 %v", hello["tags"])
	}

	// at 历史快照可读(第一条提交时只有 go/concurrency/channel)
	head := firstShort(t, ts)
	var atRow map[string]any
	getJSON(t, ts, "/api/v1/note?path=go/concurrency/channel&at="+head, &atRow)
	if atRow["title"] != "通道" {
		t.Fatalf("at=头快照应读到同一条:%v", atRow)
	}

	for _, tc := range []struct {
		path string
		code int
		desc string
	}{
		{"", http.StatusBadRequest, "缺 path"},
		{"a//b", http.StatusBadRequest, "路径段为空"},
		{"../x", http.StatusBadRequest, "路径含保留段"},
		{"go", http.StatusBadRequest, "路径是目录(类型冲突)"},
		{"no/such/note", http.StatusNotFound, "条目不存在"},
	} {
		res, body := do(t, ts, http.MethodGet, "/api/v1/note?path="+tc.path)
		if res.StatusCode != tc.code {
			t.Fatalf("note path=%q 应 %d(%s),得到 %d:%s", tc.path, tc.code, tc.desc, res.StatusCode, body)
		}
		var e map[string]any
		if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil {
			t.Fatalf("错误响应应为 {\"error\":…},得到 %s", body)
		}
	}
	res, body := do(t, ts, http.MethodGet, "/api/v1/note?path=hello&at=deadbeefdeadbeef")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("at 不存在应 404,得到 %d:%s", res.StatusCode, body)
	}
}

func TestServeSearch(t *testing.T) {
	ts := newTestServer(t)
	var rows []map[string]any
	getJSON(t, ts, "/api/v1/search?q=chan", &rows)
	if len(rows) != 1 {
		t.Fatalf("q=chan 应命中 1 条,得到 %d:%v", len(rows), rows)
	}
	hit := rows[0]
	for _, key := range []string{"path", "slug", "addr", "title", "tags", "summary", "score"} {
		if _, ok := hit[key]; !ok {
			t.Fatalf("search 行缺字段 %q:%v", key, hit)
		}
	}
	if hit["path"] != "go/concurrency/channel" || hit["title"] != "通道" {
		t.Fatalf("命中应为通道笔记:%v", hit)
	}

	// 确定性:同查询两次结果与顺序一致;limit 只截断不重排
	var again []map[string]any
	getJSON(t, ts, "/api/v1/search?q=语义", &again)
	getJSON(t, ts, "/api/v1/search?q=语义", &rows)
	if len(rows) != len(again) || len(rows) != 2 {
		t.Fatalf("q=语义 应稳定命中 2 条,得到 %d/%d", len(rows), len(again))
	}
	for i := range rows {
		if rows[i]["path"] != again[i]["path"] {
			t.Fatalf("同查询两次顺序应一致:%v vs %v", rows, again)
		}
	}
	first := rows[0]["path"]
	var limited []map[string]any
	getJSON(t, ts, "/api/v1/search?q=语义&limit=1", &limited)
	if len(limited) != 1 || limited[0]["path"] != first {
		t.Fatalf("limit=1 应只截断不重排:%v", limited)
	}

	// 空结果为 [] 而非 null
	var none []map[string]any
	getJSON(t, ts, "/api/v1/search?q=绝无仅有词zz", &none)
	if none == nil || len(none) != 0 {
		t.Fatalf("无命中应为 [],得到 %v", none)
	}

	for _, tc := range []struct {
		query string
		code  int
		desc  string
	}{
		{"", http.StatusBadRequest, "缺 q"},
		{"chan&limit=0", http.StatusBadRequest, "limit=0"},
		{"chan&limit=x", http.StatusBadRequest, "limit 非数字"},
		{"chan&at=deadbeefdeadbeef", http.StatusNotFound, "at 不存在"},
	} {
		res, body := do(t, ts, http.MethodGet, "/api/v1/search?q="+tc.query)
		if res.StatusCode != tc.code {
			t.Fatalf("search %q 应 %d(%s),得到 %d:%s", tc.query, tc.code, tc.desc, res.StatusCode, body)
		}
	}
}

func TestServeLog(t *testing.T) {
	ts := newTestServer(t)
	var rows []map[string]any
	getJSON(t, ts, "/api/v1/log", &rows)
	if len(rows) != 3 {
		t.Fatalf("种入 3 条应有 3 个快照,得到 %d", len(rows))
	}
	row := rows[0]
	for _, key := range []string{"id", "time", "message", "parents"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("log 行缺字段 %q:%v", key, row)
		}
	}
	id, _ := row["id"].(string)
	if len(id) != 16 {
		t.Fatalf("id 应为 16 字符短标识,得到 %q", id)
	}
	if row["message"] != "add hello" {
		t.Fatalf("最新快照消息应为 add hello,得到 %v", row["message"])
	}
	timeStr, _ := row["time"].(string)
	if len(timeStr) != len("2006-01-02 15:04:05") {
		t.Fatalf("time 应与 CLI log 同格式,得到 %q", timeStr)
	}
	parents, _ := row["parents"].([]any)
	if len(parents) != 1 {
		t.Fatalf("第二、三个快照应有 1 个父,得到 %v", parents)
	}
	if p, _ := parents[0].(string); len(p) != 16 {
		t.Fatalf("parents 应为短标识数组,得到 %q", p)
	}
	// 最新在前:末条(首个快照)无父
	if last, _ := rows[2]["parents"].([]any); len(last) != 0 {
		t.Fatalf("首个快照应无父(parents=[]),得到 %v", rows[2]["parents"])
	}

	var limited []map[string]any
	getJSON(t, ts, "/api/v1/log?limit=1", &limited)
	if len(limited) != 1 || limited[0]["id"] != id {
		t.Fatalf("limit=1 应只保留最新 1 条:%v", limited)
	}
	res, body := do(t, ts, http.MethodGet, "/api/v1/log?limit=-2")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit 负数应 400,得到 %d:%s", res.StatusCode, body)
	}
}

func TestServeDiff(t *testing.T) {
	ts := newTestServer(t)
	var logRows []map[string]any
	getJSON(t, ts, "/api/v1/log", &logRows)
	base, _ := logRows[len(logRows)-1]["id"].(string) // 首个快照(仅 channel 笔记)

	var rows []map[string]any
	getJSON(t, ts, "/api/v1/diff?from="+base+"&to=main", &rows)
	if len(rows) != 2 {
		t.Fatalf("首快照→main 应新增 2 条(go/io/reader、hello),得到 %v", rows)
	}
	for _, r := range rows {
		if r["type"] != "added" {
			t.Fatalf("应全部为 added,得到 %v", r)
		}
		if _, ok := r["from"]; !ok {
			t.Fatalf("diff 行缺字段 from:%v", r)
		}
	}
	if rows[0]["path"] != "go/io/reader" || rows[1]["path"] != "hello" {
		t.Fatalf("diff 应按路径字典序:%v", rows)
	}

	for _, tc := range []struct {
		query string
		code  int
		desc  string
	}{
		{"from=main", http.StatusBadRequest, "缺 to"},
		{"to=main", http.StatusBadRequest, "缺 from"},
		{"from=main&to=deadbeefdeadbeef", http.StatusNotFound, "to 不存在"},
	} {
		res, body := do(t, ts, http.MethodGet, "/api/v1/diff?"+tc.query)
		if res.StatusCode != tc.code {
			t.Fatalf("diff %q 应 %d(%s),得到 %d:%s", tc.query, tc.code, tc.desc, res.StatusCode, body)
		}
	}
}

func TestServeReadOnlyMethods(t *testing.T) {
	ts := newTestServer(t)
	// 只读端点:非 GET 一律 405 + JSON 错误 + Allow: GET
	for _, path := range []string{"/healthz", "/api/v1/projects", "/api/v1/tree", "/api/v1/search", "/api/v1/log", "/api/v1/diff"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			res, body := do(t, ts, method, path)
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s 应 405,得到 %d:%s", method, path, res.StatusCode, body)
			}
			if res.Header.Get("Allow") != "GET" {
				t.Fatalf("%s %s 应带 Allow: GET,得到 %q", method, path, res.Header.Get("Allow"))
			}
			var e map[string]any
			if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil {
				t.Fatalf("405 响应应为 {\"error\":…},得到 %s", body)
			}
		}
	}
	// /api/v1/note 是写端点宿主(§8.6):未配置令牌时 POST/DELETE 一律 403(纯只读降级);
	// 其余方法(PUT/PATCH 等)仍是 405 + Allow
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		res, body := do(t, ts, method, "/api/v1/note")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s /api/v1/note(未配置令牌)应 403,得到 %d:%s", method, res.StatusCode, body)
		}
		var e map[string]any
		if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil || !strings.Contains(e["error"].(string), "只读模式") {
			t.Fatalf("403 响应应为只读模式提示,得到 %s", body)
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		res, body := do(t, ts, method, "/api/v1/note")
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/v1/note 应 405,得到 %d:%s", method, res.StatusCode, body)
		}
		if res.Header.Get("Allow") != "GET, POST, DELETE" {
			t.Fatalf("%s /api/v1/note 应带 Allow: GET, POST, DELETE,得到 %q", method, res.Header.Get("Allow"))
		}
	}
}
