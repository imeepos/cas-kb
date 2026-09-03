package repo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imeepos/cas-kb/internal/embed"
)

// M6-C:跨提供者不同址钉死——同文本、同向量数值,仅 model 名不同
// (Ollama 适配器 vs OpenAI 兼容假端点),RebuildEmbeddings 产出的
// 向量根对象地址必不同:向量内容含 model 名,跨提供者/跨模型天然不同址,
// 无需任何额外隔离处理(DESIGN §7.3 红线 3)。
func TestEmbedCrossProviderDifferentAddress(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "embed_crossprov")
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel 并发", Body: "chan 语义"}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "misc/other", NoteInput{Title: "Other", Body: "别的"}, "b"); err != nil {
		t.Fatal(err)
	}

	// 两假端点对同一输入返回逐字节相同的向量数值(第 i 条 → [i+1, i+1])
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var er struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(req.Body).Decode(&er); err != nil {
			t.Errorf("解析 ollama 请求: %v", err)
		}
		rows := make([][]float32, len(er.Input))
		for i := range rows {
			rows[i] = []float32{float32(i + 1), float32(i + 1)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": er.Model, "embeddings": rows})
	}))
	defer ollamaSrv.Close()
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var er struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(req.Body).Decode(&er); err != nil {
			t.Errorf("解析 openai 请求: %v", err)
		}
		items := make([]struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}, len(er.Input))
		for i := range items {
			items[i].Index = i
			items[i].Embedding = []float32{float32(i + 1), float32(i + 1)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": er.Model, "data": items})
	}))
	defer openaiSrv.Close()

	o := embed.NewOllama(ollamaSrv.URL, "nomic-embed-text")
	oa := embed.NewOpenAI(openaiSrv.URL, "sk-test-t58", "text-embedding-3-small")
	if o.Model() == oa.Model() {
		t.Fatalf("两提供者模型名应不同(测试前提): %q", o.Model())
	}

	_, vecOllama, err := r.RebuildEmbeddings(ctx, o, "embed ollama")
	if err != nil {
		t.Fatal(err)
	}
	_, vecOpenAI, err := r.RebuildEmbeddings(ctx, oa, "embed openai")
	if err != nil {
		t.Fatal(err)
	}
	if vecOllama == vecOpenAI {
		t.Fatalf("同文本同向量数值、仅 model 名不同 → 向量根地址应不同: %q", vecOllama)
	}
}
