package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M6-C:KB_EMBED_PROVIDER=openai 的 CLI 全链路(本地 httptest 假 OpenAI
// 端点,零外网依赖)——index rebuild --embed 走 /v1/embeddings + Bearer 头,
// search --hybrid 用同一提供者完成查询嵌入;key 不出现在任何输出里。
func TestEmbedOpenAIRebuildHybridCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "go/channel", "Channel 并发")

	const key = "sk-t58-cli-secret"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("应请求 /v1/embeddings,实际 %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析嵌入请求: %v", err)
		}
		// 同义向量:全部输入映射到同一方向,hybrid 语义腿必命中
		items := make([]struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))
		for i := range items {
			items[i].Index = i
			items[i].Embedding = []float32{1, 0.5}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "data": items})
	}))
	defer srv.Close()
	t.Setenv("KB_EMBED_PROVIDER", "openai")
	t.Setenv("KB_EMBED_MODEL", "text-embedding-3-small")
	t.Setenv("OPENAI_API_KEY", key)
	t.Setenv("OPENAI_BASE_URL", srv.URL)

	out, err := captureStdout(t, func() error {
		return cmdIndex(ctx, []string{"rebuild", "--embed"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "vec sha256:") {
		t.Fatalf("rebuild --embed 输出应含 vec 地址: %q", out)
	}
	if gotAuth != "Bearer "+key {
		t.Fatalf("假端点应收到的鉴权头不匹配: %q", gotAuth)
	}
	if strings.Contains(out, key) {
		t.Fatalf("安全红线:输出不得回显 OPENAI_API_KEY: %q", out)
	}

	searchOut, err := captureStdout(t, func() error {
		return cmdSearch(ctx, []string{"并发", "--hybrid"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchOut, "go/channel") {
		t.Fatalf("openai 提供者 hybrid 检索应命中: %q", searchOut)
	}
	if strings.Contains(searchOut, key) {
		t.Fatalf("安全红线:检索输出不得回显 OPENAI_API_KEY: %q", searchOut)
	}
}
