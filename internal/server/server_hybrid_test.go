package server

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// hashEmbedder 是服务器侧假嵌入器:向量由(文本, 分量下标)FNV 哈希派生,
// 同文本恒同向量(确定性),零外网。
type hashEmbedder struct{}

func (hashEmbedder) Model() string { return "fake-embed-model" }
func (hashEmbedder) Dim() int      { return 4 }

func (hashEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 4)
		for j := range v {
			h := fnv.New32a()
			_, _ = h.Write([]byte(t))
			_, _ = h.Write([]byte{byte(j)})
			v[j] = float32(h.Sum32()%89) / 89
		}
		out[i] = v
	}
	return out, nil
}

// otherModelEmbedder 与 hashEmbedder 同向量算法但报不同模型名
// (制造「快照向量模型 ≠ 当前 Embedder 模型」)。
type otherModelEmbedder struct{ hashEmbedder }

func (otherModelEmbedder) Model() string { return "another-model" }

// failingHTTPEmbedder 恒定失败(模型名与建库时一致,隔离「调用失败」路径)。
type failingHTTPEmbedder struct{}

func (failingHTTPEmbedder) Model() string { return "fake-embed-model" }
func (failingHTTPEmbedder) Dim() int      { return 4 }
func (failingHTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errors.New("模拟嵌入服务不可达")
}

// newHybridTestServer 构造混合检索测试服务:withVec=true 时先以 buildEmb
// 重建向量索引;服务实例挂 serveEmb(nil=未配置嵌入服务)。
func newHybridTestServer(t *testing.T, buildEmb, serveEmb embed.Embedder, withVec bool) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := repo.Open(s, repo.Config{})
	for _, in := range []struct{ path, title, body string }{
		{"go/concurrency/channel", "通道", "chan 语义与 select"},
		{"hello", "你好", "第一条笔记"},
	} {
		if _, _, err := r.SetNote(ctx, in.path, repo.NoteInput{Title: in.title, Body: in.body}, "add "+in.path); err != nil {
			t.Fatalf("种入 %s: %v", in.path, err)
		}
	}
	if withVec {
		if _, _, err := r.RebuildEmbeddings(ctx, buildEmb, "embed rebuild"); err != nil {
			t.Fatalf("重建向量: %v", err)
		}
	}
	srv, err := New(ctx, Options{DSN: dsn, Embedder: serveEmb})
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

// newFailingEmbedServer 建库用可用嵌入器,服务实例换恒失败的嵌入器
// (模型名一致,隔离出「查询嵌入调用失败」路径)。
func newFailingEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := repo.Open(s, repo.Config{})
	if _, _, err := r.SetNote(ctx, "go/concurrency/channel", repo.NoteInput{Title: "通道", Body: "chan 语义"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, hashEmbedder{}, "embed"); err != nil {
		t.Fatal(err)
	}
	srv, err := New(ctx, Options{DSN: dsn, Embedder: failingHTTPEmbedder{}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return ts
}

// errBody 解析 {"error":"…"} 响应体。
func errBody(t *testing.T, raw []byte) string {
	t.Helper()
	var e map[string]string
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("错误响应应为 {\"error\":…}: %s", raw)
	}
	return e["error"]
}

// TestServeSearchHybridModeOK M6-B:mode=hybrid 与 CLI --hybrid 同语义——
// 行内 mode:"hybrid"、score 为 RRF 融合分(≤ 2/61)、缺省响应无 mode 字段
// (向后兼容)、重复调用逐字节一致(确定性)。
func TestServeSearchHybridModeOK(t *testing.T) {
	ts := newHybridTestServer(t, hashEmbedder{}, hashEmbedder{}, true)

	res, raw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mode=hybrid 应 200,得到 %d: %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "\"mode\": \"hybrid\"") {
		t.Fatalf("行内应带 mode 字段: %s", raw)
	}
	var rows []view.SearchRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].Mode != "hybrid" || rows[0].Path != "go/concurrency/channel" {
		t.Fatalf("mode=hybrid 应命中通道笔记且带 mode: %+v", rows)
	}
	if rows[0].Score <= 0 || rows[0].Score > 2.0/61.0 {
		t.Fatalf("RRF 融合分应在 (0, 2/61]: %v", rows[0].Score)
	}
	// 缺省响应:无 mode 字段,契约与旧消费者零破坏
	res2, raw2 := do(t, ts, http.MethodGet, "/api/v1/search?q=chan")
	if res2.StatusCode != http.StatusOK || strings.Contains(string(raw2), "\"mode\"") {
		t.Fatalf("缺省响应应 200 且无 mode 字段: %d %s", res2.StatusCode, raw2)
	}
	// 确定性:重复调用逐字节一致
	_, raw3 := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if string(raw3) != string(raw) {
		t.Fatal("mode=hybrid 响应应逐字节一致(同快照+同向量)")
	}
}

// TestServeSearchHybridErrors M6-B 失败语义(与 CLI 同文案,状态码钉死):
// 未配置 Embedder → 409 + KB_EMBED_MODEL 设置方法与 rebuild 指引,且缺省
// 检索不受影响;快照无向量 → 409 + rebuild --embed 指引;非法 mode → 400。
func TestServeSearchHybridErrors(t *testing.T) {
	// 1) 服务未配置嵌入服务(KB_EMBED_* 未设置),快照有向量:按请求 409
	ts := newHybridTestServer(t, hashEmbedder{}, nil, true)
	res, raw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("未配置 Embedder 应 409,得到 %d: %s", res.StatusCode, raw)
	}
	msg := errBody(t, raw)
	for _, want := range []string{"KB_EMBED_MODEL", "export KB_EMBED_MODEL", "rebuild --embed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("未配置报错应含 %q: %s", want, msg)
		}
	}
	// 缺省检索不受嵌入配置影响
	res2, raw2 := do(t, ts, http.MethodGet, "/api/v1/search?q=chan")
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(raw2), "go/concurrency/channel") {
		t.Fatalf("缺省检索不受嵌入配置影响: %d %s", res2.StatusCode, raw2)
	}

	// 2) 配置了 Embedder 但快照无向量:409 + rebuild --embed 指引
	tsNoVec := newHybridTestServer(t, hashEmbedder{}, hashEmbedder{}, false)
	res4, raw4 := do(t, tsNoVec, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if res4.StatusCode != http.StatusConflict {
		t.Fatalf("快照无向量应 409,得到 %d: %s", res4.StatusCode, raw4)
	}
	if msg4 := errBody(t, raw4); !strings.Contains(msg4, "无向量索引") || !strings.Contains(msg4, "kb index rebuild --embed") {
		t.Fatalf("无向量报错应含 rebuild 指引: %s", msg4)
	}

	// 3) 非法 mode 取值:400(响亮拒绝,不静默当词法)
	res5, raw5 := do(t, tsNoVec, http.MethodGet, "/api/v1/search?q=chan&mode=lexical")
	if res5.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400,得到 %d: %s", res5.StatusCode, raw5)
	}
	if msg5 := errBody(t, raw5); !strings.Contains(msg5, "mode") {
		t.Fatalf("非法 mode 报错应说明参数: %s", msg5)
	}
}

// TestServeSearchHybridModelMismatch M6-B:快照向量模型与 Embedder 模型
// 不一致 → 409 + 指引重跑 rebuild --embed(模型换了要重建)。
func TestServeSearchHybridModelMismatch(t *testing.T) {
	// 建库向量由 fake-embed-model 生成,服务实例报 another-model
	ctx := context.Background()
	dsn := freshDSN(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := repo.Open(s, repo.Config{})
	if _, _, err := r.SetNote(ctx, "go/concurrency/channel", repo.NoteInput{Title: "通道", Body: "chan 语义"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, hashEmbedder{}, "embed"); err != nil {
		t.Fatal(err)
	}
	srv, err := New(ctx, Options{DSN: dsn, Embedder: otherModelEmbedder{}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	res, raw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("模型不一致应 409,得到 %d: %s", res.StatusCode, raw)
	}
	msg := errBody(t, raw)
	if !strings.Contains(msg, "kb index rebuild --embed") || !strings.Contains(msg, "不一致") {
		t.Fatalf("模型不一致报错应含 rebuild 指引: %s", msg)
	}
}

// TestServeSearchHybridEmbedFail M6-B:查询嵌入调用失败 → 409,响亮不降级
// (绝不静默回退纯词法)。
func TestServeSearchHybridEmbedFail(t *testing.T) {
	ts := newFailingEmbedServer(t)
	res, raw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&mode=hybrid")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("嵌入失败应 409,得到 %d: %s", res.StatusCode, raw)
	}
	if msg := errBody(t, raw); !strings.Contains(msg, "嵌入") {
		t.Fatalf("嵌入失败报错应说明嵌入环节: %s", msg)
	}
}
