package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// M6-A:Ollama /api/embed 契约——请求体字段 {"model","input"}(数组形态),
// 响应 embeddings 按输入顺序一一对应;Dim() 在首次成功后缓存服务端维度。
func TestVectorEmbedOllamaBatchRequestAndOrder(t *testing.T) {
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("应请求 /api/embed,实际 %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("解析请求体: %v", err)
		}
		// 按输入顺序返回可区分的向量:第 i 条全为 float32(i+1)
		rows := make([][]float32, len(gotReq.Input))
		for i := range rows {
			rows[i] = []float32{float32(i + 1), float32(i + 1)}
		}
		_ = json.NewEncoder(w).Encode(embedResponse{Model: gotReq.Model, Embeddings: rows})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	if o.Dim() != 0 {
		t.Fatal("首次 Embed 前 Dim 应为 0")
	}
	ctx := context.Background()
	vecs, err := o.Embed(ctx, []string{"第一条", "第二条", "第三条"})
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.Model != "test-model" {
		t.Fatalf("请求体 model 字段不符: %q", gotReq.Model)
	}
	if len(gotReq.Input) != 3 || gotReq.Input[0] != "第一条" {
		t.Fatalf("请求体 input 应为字符串数组且保序: %+v", gotReq.Input)
	}
	if len(vecs) != 3 {
		t.Fatalf("应返回 3 条向量: %d", len(vecs))
	}
	for i, v := range vecs {
		want := float32(i + 1)
		if len(v) != 2 || math.Abs(float64(v[0]-want)) > 1e-6 {
			t.Fatalf("第 %d 条向量应与输入顺序对应: %v", i, v)
		}
	}
	if o.Dim() != 2 {
		t.Fatalf("成功后应缓存维度 2: %d", o.Dim())
	}
	// 空输入是合法空操作
	if v, err := o.Embed(ctx, nil); err != nil || len(v) != 0 {
		t.Fatalf("空输入应返回空切片: %v %v", v, err)
	}
}

// M6-A:超时错误文案可行动——提及超时与检查服务负载(注入短 ctx 截止)。
func TestVectorEmbedTimeoutActionableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte(`{"embeddings":[[0.1]]}`))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := o.Embed(ctx, []string{"x"})
	if err == nil {
		t.Fatal("超时应报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "超时") || !strings.Contains(msg, "负载") || !strings.Contains(msg, srv.URL) {
		t.Fatalf("超时错误应可行动(超时+服务地址+检查负载): %v", msg)
	}
}

// M6-A:HTTP 非 200 错误文案可行动——含状态码、响应片段与 ollama pull 指引。
func TestVectorEmbedHTTPErrorActionableText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model 'test-model' not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	_, err := o.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("404 应报错")
	}
	msg := err.Error()
	for _, want := range []string{"404", "ollama pull test-model", "KB_EMBED_MODEL"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误应含 %q: %v", want, msg)
		}
	}
}

// M6-A:连接失败错误文案可行动——含地址、ollama serve 与配置项指引。
func TestVectorEmbedConnectRefusedActionableText(t *testing.T) {
	// 绑定后立刻关闭,拿到一个必然拒绝连接的本地端口
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	o := NewOllama(url, "test-model")
	_, err := o.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("连接失败应报错")
	}
	msg := err.Error()
	for _, want := range []string{"无法连接嵌入服务", url, "ollama serve", "KB_EMBED_URL"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误应含 %q: %v", want, msg)
		}
	}
}

// M6-A:批量数不符响亮拒绝(服务端批量语义异常不静默截断)。
func TestVectorEmbedCountMismatchRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"test-model","embeddings":[[0.1,0.2]]}`))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	_, err := o.Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "2 条") {
		t.Fatalf("数量不符应报错并说明数量: %v", err)
	}
}

// M6-A:KB_EMBED_MODEL 未设置 = 功能整体关闭,FromEnv 给可行动报错;
// 设置后构造适配器且 KB_EMBED_URL 缺省为本地默认地址。
func TestVectorEmbedFromEnvUnconfiguredActionableError(t *testing.T) {
	t.Setenv("KB_EMBED_MODEL", "")
	t.Setenv("KB_EMBED_URL", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("KB_EMBED_MODEL 未设置应报错(不静默跳过)")
	}
	msg := err.Error()
	for _, want := range []string{"KB_EMBED_MODEL", "整体关闭", "ollama pull", "kb index rebuild --embed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("未配置错误应含 %q: %v", want, msg)
		}
	}

	t.Setenv("KB_EMBED_MODEL", "nomic-embed-text")
	o, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if o.Model() != "nomic-embed-text" || o.baseURL != DefaultURL {
		t.Fatalf("配置后应构造适配器: model=%q url=%q", o.Model(), o.baseURL)
	}

	t.Setenv("KB_EMBED_URL", "http://embed.example.com:9000/")
	o2, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if o2.baseURL != "http://embed.example.com:9000" {
		t.Fatalf("KB_EMBED_URL 应覆盖默认并去尾斜杠: %q", o2.baseURL)
	}
}
