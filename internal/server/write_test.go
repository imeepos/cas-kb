package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

// writeTestToken 是写入测试用的令牌值(测试私有,不参与任何线上路径)。
const writeTestToken = "test-write-token-123"

// newWriteTestServer 派生全新库并返回一个配置了写入令牌的 httptest 服务
// 与底层 repo(白盒测试用,可直连做种目录等操作)。
// seed 为 true 时先种入一条 a1 笔记(供删除/读回/检索断言)。
func newWriteTestServer(t *testing.T, token string, seed bool) (*httptest.Server, *repo.Repo) {
	t.Helper()
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := repo.Open(s, repo.Config{})
	if seed {
		if _, _, err := r.SetNote(ctx, "a1", repo.NoteInput{Title: "AAA", Body: "body aaa", Tags: []string{"x"}}, "add a1"); err != nil {
			t.Fatalf("种入 a1: %v", err)
		}
	}
	srv, err := New(ctx, Options{DSN: dsn, Token: token})
	if err != nil {
		t.Fatalf("构造 server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return ts, r
}

// doJSON 发送请求并返回响应与响应体;body 为 nil 时不带请求体,authToken 为空时不带鉴权头。
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any, authToken string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

// noteJSON 构造 POST /api/v1/note 的请求体。
func noteJSON(path, title, body string) map[string]string {
	return map[string]string{"path": path, "title": title, "body": body}
}

// TestServeWriteAuthMatrix:鉴权矩阵——未配置令牌 POST/DELETE 403;
// 配置令牌后缺头 401、错令牌 401、对令牌 POST 201;响应不回显令牌;读端点不鉴权。
func TestServeWriteAuthMatrix(t *testing.T) {
	// 未配置令牌:写端点一律 403(纯只读降级),文案含「只读模式」
	ro, _ := newWriteTestServer(t, "", false)
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		res, body := doJSON(t, ro, method, "/api/v1/note", noteJSON("x", "X", "b"), "")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s(无令牌)应 403,得到 %d:%s", method, res.StatusCode, body)
		}
		var e map[string]any
		if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil || !strings.Contains(e["error"].(string), "只读模式") {
			t.Fatalf("403 应为只读模式提示,得到 %s", body)
		}
	}

	// 配置令牌:缺头 401、错令牌 401、对令牌 POST 201
	rw, _ := newWriteTestServer(t, writeTestToken, false)
	for _, tc := range []struct {
		name  string
		token string
		code  int
	}{
		{"缺 Authorization 头", "", http.StatusUnauthorized},
		{"错令牌", "wrong-token", http.StatusUnauthorized},
		{"对令牌", writeTestToken, http.StatusCreated},
	} {
		res, body := doJSON(t, rw, http.MethodPost, "/api/v1/note",
			noteJSON("auth/n1", "鉴权", "content"), tc.token)
		if res.StatusCode != tc.code {
			t.Fatalf("POST(令牌=%s)应 %d(%s),得到 %d:%s", tc.token, tc.code, tc.name, res.StatusCode, body)
		}
	}
	// 401 响应不得回显令牌
	res, body := doJSON(t, rw, http.MethodPost, "/api/v1/note",
		noteJSON("auth/n2", "X", "b"), "wrong-token")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错令牌应 401,得到 %d:%s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "wrong-token") || strings.Contains(string(body), writeTestToken) {
		t.Fatalf("401 响应不得回显令牌: %s", body)
	}
	// 读端点不受鉴权影响(本机约定):无头 GET 仍 200
	res, body = doJSON(t, rw, http.MethodGet, "/api/v1/note?path=auth/n1", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读端点不应鉴权,得到 %d:%s", res.StatusCode, body)
	}
}

// TestServeWriteReadback:POST 写入后 GET 读回逐字段一致。
func TestServeWriteReadback(t *testing.T) {
	ctx := context.Background()
	ts, r := newWriteTestServer(t, writeTestToken, false)
	res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note",
		map[string]any{"path": "go/io/reader", "title": "读", "tags": []string{"go", "io"}, "body": "io reader 语义与 select"}, writeTestToken)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST 应 201,得到 %d:%s", res.StatusCode, body)
	}
	// POST 后 fsck 必须可过(不产生半写状态)
	assertFSCKClean(t, ctx, r)
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"path", "address", "short"} {
		if _, ok := created[key]; !ok {
			t.Fatalf("201 响应缺字段 %q:%s", key, body)
		}
	}
	if created["path"] != "go/io/reader" {
		t.Fatalf("path 应回显,得到 %v", created["path"])
	}
	if addr, _ := created["address"].(string); !strings.HasPrefix(addr, "sha256:") {
		t.Fatalf("address 应为 sha256 地址,得到 %v", created["address"])
	}
	if short, _ := created["short"].(string); len(short) != 16 {
		t.Fatalf("short 应为 16 字符短标识,得到 %q", short)
	}

	var row map[string]any
	getJSON(t, ts, "/api/v1/note?path=go/io/reader", &row)
	if row["path"] != "go/io/reader" || row["title"] != "读" {
		t.Fatalf("读回应逐字段一致,得到 %v", row)
	}
	if row["body"] != "io reader 语义与 select" {
		t.Fatalf("读回正文不一致: %v", row["body"])
	}
	tags, _ := row["tags"].([]any)
	if len(tags) != 2 || tags[0] != "go" || tags[1] != "io" {
		t.Fatalf("读回 tags 不一致: %v", row["tags"])
	}
}

// TestServeWriteSearchImmediate:POST 写入后立即可 search(索引增量同步完成)。
func TestServeWriteSearchImmediate(t *testing.T) {
	ts, _ := newWriteTestServer(t, writeTestToken, false)
	res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note",
		noteJSON("topic/alpha", "阿尔法通道", "BM25 检索立即可见"), writeTestToken)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST 应 201,得到 %d:%s", res.StatusCode, body)
	}
	var rows []map[string]any
	getJSON(t, ts, "/api/v1/search?q=阿尔法", &rows)
	if len(rows) != 1 || rows[0]["path"] != "topic/alpha" {
		t.Fatalf("写入后应立即可检索,得到 %v", rows)
	}
}

// TestServeWriteBadPath:非法路径/缺参 → 400(沿用 CLI 可行动报错文案)。
func TestServeWriteBadPath(t *testing.T) {
	ts, _ := newWriteTestServer(t, writeTestToken, true) // 种入 a1(条目)
	for _, tc := range []struct {
		name string
		body map[string]string
	}{
		{"缺 path", map[string]string{"title": "X", "body": "b"}},
		{"缺 title", map[string]string{"path": "p", "body": "b"}},
	} {
		res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note", tc.body, writeTestToken)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s 应 400,得到 %d:%s", tc.name, res.StatusCode, body)
		}
		var e map[string]any
		if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil {
			t.Fatalf("%s 应为 {\"error\":…},得到 %s", tc.name, body)
		}
	}
	// 非法路径:空段、保留段
	for _, path := range []string{"a//b", "../x"} {
		res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note", noteJSON(path, "X", "b"), writeTestToken)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST path=%q 应 400,得到 %d:%s", path, res.StatusCode, body)
		}
	}
	// 中间段是条目(a1 是条目)→ 400
	res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note", noteJSON("a1/sub", "X", "b"), writeTestToken)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("中间段是条目应 400,得到 %d:%s", res.StatusCode, body)
	}
	// 目标是目录:先种一个目录(直接 repo.Mkdir),再 POST 同名路径 → 400
	ts, r := newWriteTestServer(t, writeTestToken, true)
	ctx := context.Background()
	if _, _, err := r.Mkdir(ctx, "adir", "make dir"); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	res, body = doJSON(t, ts, http.MethodPost, "/api/v1/note", noteJSON("adir", "X", "b"), writeTestToken)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("目标是目录应 400,得到 %d:%s", res.StatusCode, body)
	}
	// DELETE 缺 path → 400
	res, body = doJSON(t, ts, http.MethodDelete, "/api/v1/note", nil, writeTestToken)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("DELETE 缺 path 应 400,得到 %d:%s", res.StatusCode, body)
	}
}

// TestServeWriteDelete:DELETE 后 404、再 GET 404(等价 CLI note rm)。
func TestServeWriteDelete(t *testing.T) {
	ctx := context.Background()
	ts, r := newWriteTestServer(t, writeTestToken, true) // 种入 a1
	res, body := doJSON(t, ts, http.MethodDelete, "/api/v1/note?path=a1", nil, writeTestToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("DELETE 应 200,得到 %d:%s", res.StatusCode, body)
	}
	// DELETE 后 fsck 必须可过
	assertFSCKClean(t, ctx, r)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["removed"] != float64(1) || out["short"] == "" {
		t.Fatalf("DELETE 应返回 {removed:1,short},得到 %s", body)
	}
	res, body = doJSON(t, ts, http.MethodGet, "/api/v1/note?path=a1", nil, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("删除后 GET 应 404,得到 %d:%s", res.StatusCode, body)
	}
	res, body = doJSON(t, ts, http.MethodDelete, "/api/v1/note?path=a1", nil, writeTestToken)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("删除不存在的条目应 404,得到 %d:%s", res.StatusCode, body)
	}
}

// assertFSCKClean 断言全库巡检零问题(写端点不产生半写状态)。
func assertFSCKClean(t *testing.T, ctx context.Context, r *repo.Repo) {
	t.Helper()
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatalf("fsck: %v", err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("fsck 应零问题,得到 %v", res.Problems)
	}
}

// lockBusyStore 包装真实存储:写方法(Put/BranchSet)恒返回锁忙错误,读方法透传。
// 用于无真实并发锁时驱动 503 路径。
type lockBusyStore struct {
	store.Store
}

// Put 模拟「知识库正被其他写入占用」。
func (l *lockBusyStore) Put(ctx context.Context, kind object.Kind, data []byte) (hash.Address, error) {
	return "", errors.New("database is locked")
}

// TestServeWriteLockBusy503:后端锁忙 → 503 + 可行动提示(「稍后重试或改用 CLI」)。
// 用锁忙存储包装真实存储:repo 打开在包装之上,SetNote 首步 Put 即失败,propagate 到 failWrite。
func TestServeWriteLockBusy503(t *testing.T) {
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	busy := &lockBusyStore{Store: s}
	r := repo.Open(busy, repo.Config{})
	srv := &Server{st: busy, r: r, backend: "sqlite", target: dsn, project: r.Project(), token: writeTestToken}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	res, body := doJSON(t, ts, http.MethodPost, "/api/v1/note",
		noteJSON("lock/x", "X", "b"), writeTestToken)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("锁忙应 503,得到 %d:%s", res.StatusCode, body)
	}
	var e map[string]any
	if err := json.Unmarshal(body, &e); err != nil || e["error"] == nil {
		t.Fatalf("503 应为 {\"error\":…},得到 %s", body)
	}
	msg, _ := e["error"].(string)
	if !strings.Contains(msg, "稍后重试或改用 CLI") {
		t.Fatalf("503 应含可行动提示,得到 %q", msg)
	}
}
