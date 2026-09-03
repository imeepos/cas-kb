package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/server"
	"github.com/imeepos/cas-kb/internal/view"
)

// fakeOllama 起一个本地 httptest 假嵌入服务(Ollama /api/embed 兼容):
// 向量由(文本, 分量下标)FNV 哈希派生,同文本恒同向量(确定性),零外网。
func fakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析嵌入请求: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rows := make([][]float32, len(req.Input))
		for i, text := range req.Input {
			v := make([]float32, 4)
			for j := range v {
				h := fnv.New32a()
				_, _ = h.Write([]byte(text))
				_, _ = h.Write([]byte{byte(j)})
				v[j] = float32(h.Sum32()%97) / 97
			}
			rows[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "embeddings": rows})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSearchHybridCLI M6-B 验收:kb search --hybrid 端到端(本地假 Ollama)——
// 快照无 vec 时响亮报错(指引 rebuild --embed);rebuild --embed 后混合检索
// 可用且确定性;--json 增可选 mode:"hybrid" 字段(仅 --hybrid 时存在,与
// --snippet 可叠加);未配置 KB_EMBED_* 时报错含设置方法与 rebuild 指引。
func TestSearchHybridCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdNote(ctx, []string{"set", "go/channel", "--title", "Channel 并发", "--body", "chan 语义", "-m", "a"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"set", "misc/other", "--title", "Other", "--body", "别的", "-m", "b"}); err != nil {
		t.Fatal(err)
	}
	srv := fakeOllama(t)
	t.Setenv("KB_EMBED_URL", srv.URL)
	t.Setenv("KB_EMBED_MODEL", "fake-embed-model")

	// 1) 快照无 vec:响亮报错,文案含 rebuild --embed 指引与词法出路
	err := func() error {
		_, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--hybrid"}) })
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "无向量索引") || !strings.Contains(err.Error(), "kb index rebuild --embed") {
		t.Fatalf("快照无 vec 应报错并指引 rebuild: %v", err)
	}

	// 2) 重建向量后 hybrid 可用(文本模式)
	if _, err := captureStdout(t, func() error { return cmdIndex(ctx, []string{"rebuild", "--embed"}) }); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--hybrid"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go/channel") {
		t.Fatalf("hybrid 文本输出应命中 go/channel: %q", out)
	}
	// 确定性:同快照 + 同向量数据 → 逐字节一致
	out2, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--hybrid"}) })
	if err != nil || out != out2 {
		t.Fatalf("hybrid 输出应逐字节确定: err=%v\n%s\n%s", err, out, out2)
	}

	// 3) --json --hybrid:行内 mode:"hybrid";缺省 --json 无 mode(向后兼容)
	jout, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--hybrid", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jout, "\"mode\": \"hybrid\"") {
		t.Fatalf("--json --hybrid 应含 mode 字段: %q", jout)
	}
	var rows []view.SearchRow
	if err := json.Unmarshal([]byte(jout), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].Mode != "hybrid" {
		t.Fatalf("行契约应带 mode=hybrid: %+v", rows)
	}
	if rows[0].Score <= 0 || rows[0].Score > 2.0/61.0 {
		t.Fatalf("RRF 融合分应在 (0, 2/61] 区间: %v", rows[0].Score)
	}
	jplain, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jplain, "\"mode\"") {
		t.Fatalf("缺省 --json 不得出现 mode 字段: %q", jplain)
	}
	// 4) --hybrid --snippet 叠加:mode 与 snippet 并存
	jboth, err := captureStdout(t, func() error {
		return cmdSearch(ctx, []string{"channel", "--hybrid", "--snippet", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jboth, "\"mode\": \"hybrid\"") || !strings.Contains(jboth, "\"snippet\"") {
		t.Fatalf("--hybrid --snippet 应同时含 mode 与 snippet: %q", jboth)
	}

	// 5) 未配置 KB_EMBED_MODEL:报错含设置方法(export KB_EMBED_MODEL=…)与
	//    rebuild 指引,绝不静默降级为纯词法
	t.Setenv("KB_EMBED_MODEL", "")
	err = func() error {
		_, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--hybrid"}) })
		return err
	}()
	if err == nil {
		t.Fatal("未配置嵌入服务时 --hybrid 应报错")
	}
	for _, want := range []string{"KB_EMBED_MODEL", "export KB_EMBED_MODEL", "rebuild --embed", "词法检索"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("配置报错应含 %q: %v", want, err)
		}
	}
	// 同一快照缺省检索不受影响(BM25 默认不动)
	plain, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel"}) })
	if err != nil || !strings.Contains(plain, "go/channel") {
		t.Fatalf("缺省 BM25 检索应不受影响: err=%v out=%q", err, plain)
	}
}

// TestServeCLIParityHybrid M6-B parity:同一临时库 + 同一假嵌入服务上,
// GET /api/v1/search?mode=hybrid 与 kb search --json --hybrid 逐字段相等
// (含 mode 与 score=RRF 融合分);mode 缺省时两出口同样一致且无 mode 字段。
func TestServeCLIParityHybrid(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	for _, args := range [][]string{
		{"set", "go/concurrency/channel", "--title", "通道", "--body", "chan 语义", "-m", "add channel"},
		{"set", "go/io/reader", "--title", "读", "--body", "io reader 语义", "-m", "add reader"},
		{"set", "hello", "--title", "你好", "--body", "第一条笔记", "-m", "add hello"},
	} {
		if err := cmdNote(ctx, args); err != nil {
			t.Fatalf("种入 %v: %v", args, err)
		}
	}
	srv := fakeOllama(t)
	const model = "fake-embed-model"
	t.Setenv("KB_EMBED_URL", srv.URL)
	t.Setenv("KB_EMBED_MODEL", model)
	if _, err := captureStdout(t, func() error { return cmdIndex(ctx, []string{"rebuild", "--embed"}) }); err != nil {
		t.Fatal(err)
	}

	// serve 进程的 Embedder 与 CLI 环境指向同一假服务(同模型 → 同向量)
	emb := embed.NewOllama(srv.URL, model)
	base, stop := startEmbedServe(t, emb)
	defer stop()

	var apiRows, cliRows []view.SearchRow
	apiGetJSON(t, base+"/api/v1/search?q=chan&mode=hybrid", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "--hybrid", "--json"}) }, &cliRows)
	assertHybridParity(t, "hybrid q=chan", apiRows, cliRows)
	if len(apiRows) == 0 || apiRows[0].Path != "go/concurrency/channel" {
		t.Fatalf("hybrid 应命中通道笔记: %+v", apiRows)
	}

	// 中文查询 + limit 截断后仍逐字段一致
	apiGetJSON(t, base+"/api/v1/search?q=%E8%AF%AD%E4%B9%89&mode=hybrid&limit=2", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"语义", "--hybrid", "--json", "-n", "2"}) }, &cliRows)
	assertHybridParity(t, "hybrid 中文+limit", apiRows, cliRows)

	// mode 缺省:两出口一致且无 mode 字段(契约不变;用全新切片避免
	// json.Unmarshal 对既有元素的缺省字段不置零)
	var apiPlain, cliPlain []view.SearchRow
	apiGetJSON(t, base+"/api/v1/search?q=chan", &apiPlain)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "--json"}) }, &cliPlain)
	assertHybridParity(t, "缺省 q=chan", apiPlain, cliPlain)
	for i := range apiPlain {
		if apiPlain[i].Mode != "" {
			t.Fatalf("缺省模式不得带 mode 字段: %+v", apiPlain[i])
		}
	}
}

// assertHybridParity 断言两出口检索行逐字段(含 Mode 与 Score)与顺序相等。
func assertHybridParity(t *testing.T, name string, api, cli []view.SearchRow) {
	t.Helper()
	if len(api) != len(cli) {
		t.Fatalf("%s: 行数应相等,API %d vs CLI %d\nAPI: %+v\nCLI: %+v", name, len(api), len(cli), api, cli)
	}
	for i := range cli {
		a, c := api[i], cli[i]
		if a.Path != c.Path || a.Slug != c.Slug || a.Addr != c.Addr || a.Title != c.Title ||
			a.Summary != c.Summary || a.Snippet != c.Snippet || a.Mode != c.Mode ||
			fmt.Sprint(a.Tags) != fmt.Sprint(c.Tags) || a.Score != c.Score {
			t.Errorf("%s: 第 %d 行不等:\nAPI %+v\nCLI %+v", name, i, a, c)
		}
	}
}

// startEmbedServe 起一个带 Embedder 的 serve 实例(parity 测试用)。
func startEmbedServe(t *testing.T, emb embed.Embedder) (baseURL string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p, err := startServe(ctx, "127.0.0.1:0", serverOptionsWithEmbedder(emb))
	if err != nil {
		t.Fatalf("起 serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- p.wait(ctx) }()
	return "http://" + p.Addr().String(), func() {
		cancel()
		<-done
	}
}

// serverOptionsWithEmbedder 构造与 CLI 同口径的 serve 选项(DSN/项目/分支
// 取当前测试环境),并挂上 Embedder。
func serverOptionsWithEmbedder(emb embed.Embedder) server.Options {
	return server.Options{DSN: effectiveDSN(), Project: projectName(), Branch: branchName(), Embedder: emb}
}
