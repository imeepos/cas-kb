package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imeepos/cas-kb/internal/server"
	"github.com/imeepos/cas-kb/internal/view"
)

// oldestShort 返回最旧快照的短标识。kb log 每行格式为
// 「短标识  时间  parent=…  消息」且最新在前,故最旧快照=末行首列;
// 按行切分取首字段,不能用 Fields 全文拍平(那会取到末行 parent= 值)。
func oldestShort(t *testing.T) string {
	t.Helper()
	out, err := captureStdout(t, func() error { return cmdLog(context.Background(), nil) })
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatal("log 应至少有一个快照")
	}
	return strings.Fields(lines[len(lines)-1])[0]
}

// startAPIServe 在随机端口起一个 kb serve 实例(测试库已由 initRepo 准备),
// token 非空时为该实例配置写入令牌;返回基础 URL 与停止函数(优雅退出并等待收尾完成)。
func startAPIServe(t *testing.T, token string) (baseURL string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p, err := startServe(ctx, "127.0.0.1:0", server.Options{
		DSN: effectiveDSN(), Project: projectName(), Branch: branchName(), Token: token,
	})
	if err != nil {
		t.Fatalf("起 serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- p.wait(ctx) }()
	return "http://" + p.Addr().String(), func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("serve 收尾: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("serve 未在 10s 内优雅退出")
		}
	}
}

// apiGetJSON 拉取 URL 并把 JSON 解析到 out。
func apiGetJSON(t *testing.T, url string, out any) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 应 200,得到 %d:%s", url, res.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("GET %s 解析 JSON: %v:%s", url, err, body)
	}
}

// cliJSON 执行 CLI 命令函数并解析其 --json 输出。
func cliJSON(t *testing.T, fn func() error, out any) {
	t.Helper()
	stdout, err := captureStdout(t, fn)
	if err != nil {
		t.Fatalf("CLI 命令失败: %v", err)
	}
	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		t.Fatalf("CLI --json 解析: %v:%s", err, stdout)
	}
}

// TestServeCLIParity:M4 验收第三条——同一临时库上,只读 HTTP API 与 CLI --json
// 解析后逐字段相等;search 的结果顺序也必须相等(确定性排序不被破坏)。
func TestServeCLIParity(t *testing.T) {
	ctx := context.Background()
	initRepo(t)

	// 种子:4 个提交(含一次同路径修改,保证 diff 覆盖 A/M 两类)
	seeds := [][]string{
		{"set", "go/concurrency/channel", "--title", "通道", "--body", "chan 语义", "--tags", "go", "-m", "add channel"},
		{"set", "go/io/reader", "--title", "读", "--body", "io reader 语义", "--tags", "go", "-m", "add reader"},
		{"set", "hello", "--title", "你好", "--body", "第一条笔记", "-m", "add hello"},
		{"set", "go/concurrency/channel", "--title", "通道", "--body", "chan 语义与 select", "--tags", "go", "-m", "tweak channel"},
	}
	for _, args := range seeds {
		if err := cmdNote(ctx, args); err != nil {
			t.Fatalf("种入 %v: %v", args, err)
		}
	}
	s1 := oldestShort(t) // 最旧快照(仅 channel v1;log 最新在前,末行即首提交)

	base, stop := startAPIServe(t, "")
	defer stop()

	// ---- search:字段与顺序逐项相等 ----
	var apiRows, cliRows []view.SearchRow
	apiGetJSON(t, base+"/api/v1/search?q=chan", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "--json"}) }, &cliRows)
	assertSearchParity(t, "search q=chan", apiRows, cliRows)
	if len(apiRows) != 1 || apiRows[0].Path != "go/concurrency/channel" {
		t.Fatalf("q=chan 应命中通道笔记 1 条,得到 %v", apiRows)
	}

	// 多词查询(CLI 位置参数以空格拼接 vs API q 参数)
	apiGetJSON(t, base+"/api/v1/search?q=chan+%E8%AF%AD%E4%B9%89", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "语义", "--json"}) }, &cliRows)
	assertSearchParity(t, "search 多词", apiRows, cliRows)

	// 历史快照检索(--at 短标识)
	apiGetJSON(t, base+"/api/v1/search?q=chan&at="+s1, &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "--at", s1, "--json"}) }, &cliRows)
	assertSearchParity(t, "search --at", apiRows, cliRows)

	// limit 截断后仍一致(-n ↔ limit)
	apiGetJSON(t, base+"/api/v1/search?q=%E8%AF%AD%E4%B9%89&limit=1", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"语义", "-n", "1", "--json"}) }, &cliRows)
	assertSearchParity(t, "search limit", apiRows, cliRows)

	// 片段高亮 parity(--snippet ↔ snippet=1,M4.2):含 snippet 字段逐字段相等
	apiGetJSON(t, base+"/api/v1/search?q=chan+%E8%AF%AD%E4%B9%89&snippet=1", &apiRows)
	cliJSON(t, func() error { return cmdSearch(ctx, []string{"chan", "语义", "--json", "--snippet"}) }, &cliRows)
	assertSearchParity(t, "search snippet parity", apiRows, cliRows)
	if len(apiRows) == 0 || !strings.Contains(apiRows[0].Snippet, "【") {
		t.Fatalf("snippet=1 应附带含标记的片段: %+v", apiRows)
	}

	// ---- projects:逐字段相等 ----
	var apiProjects, cliProjects []view.ProjectRow
	apiGetJSON(t, base+"/api/v1/projects", &apiProjects)
	cliJSON(t, func() error { return cmdProject(ctx, []string{"ls", "--json"}) }, &cliProjects)
	if len(apiProjects) != len(cliProjects) {
		t.Fatalf("projects 行数应相等: API %d vs CLI %d", len(apiProjects), len(cliProjects))
	}
	for i := range cliProjects {
		if apiProjects[i] != cliProjects[i] {
			t.Fatalf("projects 第 %d 行应逐字段相等: API %+v vs CLI %+v", i, apiProjects[i], cliProjects[i])
		}
	}

	// ---- diff:逐字段相等(短标识 → 分支名;覆盖 A/M 两类)----
	var apiDiff, cliDiff []view.DiffRow
	apiGetJSON(t, base+"/api/v1/diff?from="+s1+"&to=main", &apiDiff)
	cliJSON(t, func() error { return cmdDiff(ctx, []string{s1, "main", "--json"}) }, &cliDiff)
	if len(apiDiff) != len(cliDiff) {
		t.Fatalf("diff 行数应相等: API %d vs CLI %d(%v)", len(apiDiff), len(cliDiff), apiDiff)
	}
	for i := range cliDiff {
		if apiDiff[i] != cliDiff[i] {
			t.Fatalf("diff 第 %d 行应逐字段相等: API %+v vs CLI %+v", i, apiDiff[i], cliDiff[i])
		}
	}
	if len(apiDiff) != 3 {
		t.Fatalf("首快照→main 应有 M channel + A reader + A hello 共 3 行,得到 %v", apiDiff)
	}
}

// assertSearchParity 逐字段比较 API 与 CLI 的 search 行(含顺序)。
func assertSearchParity(t *testing.T, name string, api, cli []view.SearchRow) {
	t.Helper()
	if len(api) != len(cli) {
		t.Fatalf("%s: 行数应相等,API %d vs CLI %d\nAPI: %+v\nCLI: %+v", name, len(api), len(cli), api, cli)
	}
	for i := range cli {
		a, c := api[i], cli[i]
		if a.Path != c.Path {
			t.Errorf("%s: 第 %d 行 path 不等: API %q vs CLI %q", name, i, a.Path, c.Path)
		}
		if a.Slug != c.Slug {
			t.Errorf("%s: 第 %d 行 slug 不等: API %q vs CLI %q", name, i, a.Slug, c.Slug)
		}
		if a.Addr != c.Addr {
			t.Errorf("%s: 第 %d 行 addr 不等: API %q vs CLI %q", name, i, a.Addr, c.Addr)
		}
		if a.Title != c.Title {
			t.Errorf("%s: 第 %d 行 title 不等: API %q vs CLI %q", name, i, a.Title, c.Title)
		}
		if fmt.Sprint(a.Tags) != fmt.Sprint(c.Tags) {
			t.Errorf("%s: 第 %d 行 tags 不等: API %v vs CLI %v", name, i, a.Tags, c.Tags)
		}
		if a.Summary != c.Summary {
			t.Errorf("%s: 第 %d 行 summary 不等: API %q vs CLI %q", name, i, a.Summary, c.Summary)
		}
		if a.Snippet != c.Snippet {
			t.Errorf("%s: 第 %d 行 snippet 不等: API %q vs CLI %q", name, i, a.Snippet, c.Snippet)
		}
		if a.Score != c.Score {
			t.Errorf("%s: 第 %d 行 score 不等: API %v vs CLI %v", name, i, a.Score, c.Score)
		}
	}
}

// TestServeProcessLifecycle:kb serve 起在随机端口(走完整命令路径),
// healthz 200,ctx 取消后优雅退出且端口不再响应。
func TestServeProcessLifecycle(t *testing.T) {
	initRepo(t)

	// 预留一个空闲端口(立即释放,serve 随后占用;测试环境足够稳定)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- cmdServe(ctx, []string{"--addr", addr}) }()

	base := "http://" + addr
	client := &http.Client{Timeout: 2 * time.Second}
	var res *http.Response
	ready := false
	for i := 0; i < 50; i++ {
		if res, err = client.Get(base + "/healthz"); err == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("serve 未在 5s 内就绪: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz 应 200,得到 %d:%s", res.StatusCode, body)
	}
	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("healthz 解析: %v:%s", err, body)
	}
	if health["ok"] != true {
		t.Fatalf("healthz 应 ok:true,得到 %v", health)
	}

	// 优雅退出:取消 ctx(cmdServe 的退出信号之一),应在限时内返回 nil
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve 优雅退出应返回 nil,得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve 未在 10s 内优雅退出")
	}
	if res, err = client.Get(base + "/healthz"); err == nil {
		res.Body.Close()
		t.Fatal("serve 退出后端口不应再响应")
	}
}
