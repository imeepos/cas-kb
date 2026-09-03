package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M6-A:kb index rebuild --embed 端到端(本地 httptest 假 Ollama,零外网依赖)
// ——输出 vec/snapshot 地址;fsck 通过;检索默认路径(BM25)不受影响;
// 未配置 KB_EMBED_MODEL 时给可行动报错而非静默跳过。
func TestVectorIndexRebuildEmbedCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "go/channel", "Channel 并发")
	setNote(t, "misc/other", "Other")

	var lastInput []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析嵌入请求: %v", err)
		}
		lastInput = req.Input
		rows := make([][]float32, len(req.Input))
		for i := range rows {
			rows[i] = []float32{float32(i), 0.5, -0.5, 0.25}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "embeddings": rows})
	}))
	defer srv.Close()
	t.Setenv("KB_EMBED_URL", srv.URL)
	t.Setenv("KB_EMBED_MODEL", "fake-embed-model")

	out, err := captureStdout(t, func() error {
		return cmdIndex(ctx, []string{"rebuild", "--embed"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "vec sha256:") || !strings.Contains(out, "snapshot sha256:") {
		t.Fatalf("rebuild --embed 输出应含 vec/snapshot 地址: %q", out)
	}
	// 嵌入输入是「标题+正文」拼接文本,已送达嵌入服务
	if len(lastInput) == 0 || !strings.Contains(lastInput[0], "Channel 并发") {
		t.Fatalf("嵌入输入应含标题+正文: %+v", lastInput)
	}
	// 向量库落库后 fsck 必须干净
	if err := cmdFSCK(ctx, nil); err != nil {
		t.Fatalf("fsck 应通过: %v", err)
	}
	// 默认检索仍是 BM25,行为不变
	searchOut, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchOut, "go/channel") {
		t.Fatalf("BM25 检索不受 --embed 影响: %q", searchOut)
	}

	// 未配置 KB_EMBED_MODEL:整体关闭 + 可行动报错(绝不静默跳过)
	t.Setenv("KB_EMBED_MODEL", "")
	err = func() error {
		_, err := captureStdout(t, func() error {
			return cmdIndex(ctx, []string{"rebuild", "--embed"})
		})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "KB_EMBED_MODEL") {
		t.Fatalf("未配置应给可行动报错: %v", err)
	}
}
