package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imeepos/cas-kb/internal/view"
)

// parityToken 是 CLI/API parity 测试用的写入令牌。
const parityToken = "parity-write-token"

// apiWrite 向写入端点发请求并断言期望状态码,返回响应体。
func apiWrite(t *testing.T, base, method, path, token string, body any, wantCode int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != wantCode {
		t.Fatalf("%s %s 应 %d,得到 %d:%s", method, path, wantCode, res.StatusCode, out)
	}
	return out
}

// assertNoteRowEqual 逐字段断言两条 NoteRow 相等(path/title/tags/body/summary)。
func assertNoteRowEqual(t *testing.T, name string, a, b view.NoteRow) {
	t.Helper()
	if a.Path != b.Path {
		t.Errorf("%s: path 不等: %q vs %q", name, a.Path, b.Path)
	}
	if a.Title != b.Title {
		t.Errorf("%s: title 不等: %q vs %q", name, a.Title, b.Title)
	}
	if fmt.Sprint(a.Tags) != fmt.Sprint(b.Tags) {
		t.Errorf("%s: tags 不等: %v vs %v", name, a.Tags, b.Tags)
	}
	if a.Body != b.Body {
		t.Errorf("%s: body 不等: %q vs %q", name, a.Body, b.Body)
	}
	if a.Summary != b.Summary {
		t.Errorf("%s: summary 不等: %q vs %q", name, a.Summary, b.Summary)
	}
}

// TestServeWriteCLIParity:写入型 API 与 CLI 逐字段同构——
//   - API POST 一条后,CLI note get --json 读回与 API note 读回逐字段相等;
//   - CLI note set 后,API note 读回与 CLI 读回逐字段相等;
//   - API DELETE 后,CLI note get 报「不存在」(404 语义)。
func TestServeWriteCLIParity(t *testing.T) {
	ctx := context.Background()
	initRepo(t)

	base, stop := startAPIServe(t, parityToken)
	defer stop()

	// ---- API POST 一条 → CLI note get --json 读回,字段逐项相等 ----
	out := apiWrite(t, base, http.MethodPost, "/api/v1/note", parityToken, map[string]any{
		"path":  "parity/api",
		"title": "API 写入",
		"tags":  []string{"api", "t33"},
		"body":  "经 HTTP 写入的正文",
	}, http.StatusCreated)
	var created map[string]any
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatal(err)
	}
	if created["path"] != "parity/api" {
		t.Fatalf("POST 响应 path 应回显,得到 %v", created["path"])
	}
	if _, ok := created["address"]; !ok {
		t.Fatalf("POST 响应缺 address:%s", out)
	}
	if _, ok := created["short"]; !ok {
		t.Fatalf("POST 响应缺 short:%s", out)
	}

	var apiRow, cliRow view.NoteRow
	apiGetJSON(t, base+"/api/v1/note?path=parity/api", &apiRow)
	cliJSON(t, func() error { return cmdNote(ctx, []string{"get", "parity/api", "--json"}) }, &cliRow)
	assertNoteRowEqual(t, "API POST → CLI get --json", apiRow, cliRow)

	// ---- CLI note set → API note 读回,字段逐项相等 ----
	if err := cmdNote(ctx, []string{"set", "parity/cli", "--title", "CLI 写入", "--body", "cli 写入的正文", "--tags", "cli", "-m", "cli set"}); err != nil {
		t.Fatal(err)
	}
	apiGetJSON(t, base+"/api/v1/note?path=parity/cli", &apiRow)
	cliJSON(t, func() error { return cmdNote(ctx, []string{"get", "parity/cli", "--json"}) }, &cliRow)
	assertNoteRowEqual(t, "CLI set → API note", apiRow, cliRow)

	// ---- API DELETE → CLI note get 404 语义(报不存在) ----
	apiWrite(t, base, http.MethodDelete, "/api/v1/note?path=parity/api", parityToken, nil, http.StatusOK)
	err := cmdNote(ctx, []string{"get", "parity/api"})
	if err == nil {
		t.Fatal("API DELETE 后 CLI note get 应报错(404 语义)")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("CLI note get 应报「不存在」,得到 %v", err)
	}
	// CLI 侧同路径也确认不可见(等价 404)
	if err := cmdNote(ctx, []string{"get", "parity/api", "--json"}); err == nil {
		t.Fatal("CLI note get --json 同样应报错")
	}
}
