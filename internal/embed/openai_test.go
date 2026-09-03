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

// testKey 是假 API key——测试用它钉死「key 只进请求头、绝不进错误文案」。
const testKey = "sk-t58-secret-key-DO-NOT-LEAK"

// newOpenAIFake 起一个 OpenAI 兼容假端点:校验路径/鉴权头/请求体,
// 并按 data[].index 乱序返回可区分向量(第 i 条输入 → 全 float32(i+1)),
// 用于钉死「响应按 index 归位对齐输入顺序,不信数组序」。
func newOpenAIFake(t *testing.T, mutate func(items []openAIItem)) (*httptest.Server, *embedRequest) {
	t.Helper()
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("应请求 /v1/embeddings,实际 %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization 头应为 Bearer <key>,实际 %q", got)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 应为 JSON,实际 %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("解析请求体: %v", err)
		}
		items := make([]openAIItem, len(gotReq.Input))
		for i := range items {
			// 向量取值由 index 决定:归位后应与输入顺序一一对应
			items[i] = openAIItem{Index: i, Embedding: []float32{float32(i + 1), float32(i + 1)}}
		}
		if mutate != nil {
			mutate(items)
		}
		_ = json.NewEncoder(w).Encode(openAIResponse{Model: gotReq.Model, Data: items})
	}))
	t.Cleanup(srv.Close)
	return srv, &gotReq
}

// M6-C:OpenAI 兼容契约——请求体 {"model","input":[texts]} + Bearer 鉴权头;
// 响应 data[] 故意乱序返回,Embed 必须按 index 归位对齐输入顺序。
func TestEmbedOpenAIBatchAuthHeaderAndIndexAlignment(t *testing.T) {
	// 乱序:服务端先给 index 2,再 0,再 1(数组序 ≠ 输入序)
	srv, gotReq := newOpenAIFake(t, func(items []openAIItem) {
		items[0], items[1], items[2] = items[2], items[0], items[1]
	})

	oa := NewOpenAI(srv.URL, testKey, "text-embedding-3-small")
	if oa.Dim() != 0 {
		t.Fatal("首次 Embed 前 Dim 应为 0")
	}
	ctx := context.Background()
	vecs, err := oa.Embed(ctx, []string{"第一条", "第二条", "第三条"})
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.Model != "text-embedding-3-small" {
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
			t.Fatalf("第 %d 条向量应按 index 归位(不信数组序): %v", i, v)
		}
	}
	if oa.Dim() != 2 {
		t.Fatalf("成功后应缓存维度 2: %d", oa.Dim())
	}
	// 空输入是合法空操作(不发请求)
	if v, err := oa.Embed(ctx, nil); err != nil || len(v) != 0 {
		t.Fatalf("空输入应返回空切片: %v %v", v, err)
	}
}

// M6-C:base 两种习惯(带/不带 /v1)与尾斜杠的拼接规则,规则与注释双钉死:
// 去尾斜杠后已以 /v1 结尾 → {base}/embeddings;否则 → {base}/v1/embeddings。
func TestEmbedOpenAIBaseURLJoinForms(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://host", "https://host/v1/embeddings"},
		{"https://host/v1", "https://host/v1/embeddings"},
		{"https://host/", "https://host/v1/embeddings"},
		{"https://host/v1/", "https://host/v1/embeddings"},
		{"https://gw.example.com/openai", "https://gw.example.com/openai/v1/embeddings"},
		{"https://gw.example.com/openai/v1", "https://gw.example.com/openai/v1/embeddings"},
		{DefaultOpenAIBaseURL, "https://api.openai.com/v1/embeddings"},
	}
	for _, c := range cases {
		if got := embeddingsEndpoint(c.base); got != c.want {
			t.Errorf("embeddingsEndpoint(%q) = %q,want %q", c.base, got, c.want)
		}
	}

	// 真实请求路径钉死:同一假端点,两种 base 写法都命中 /v1/embeddings
	srv, _ := newOpenAIFake(t, nil)
	ctx := context.Background()
	for _, base := range []string{srv.URL, srv.URL + "/v1"} {
		oa := NewOpenAI(base, testKey, "m")
		if _, err := oa.Embed(ctx, []string{"x"}); err != nil {
			t.Fatalf("base %q 应请求成功(端点 %s): %v", base, oa.endpoint, err)
		}
	}
}

// M6-C:OPENAI_API_KEY 缺失 = 响亮报错含设置方法;缺省 base 为官方端点。
func TestEmbedOpenAIKeyMissingActionableError(t *testing.T) {
	t.Setenv("KB_EMBED_PROVIDER", ProviderOpenAI)
	t.Setenv("KB_EMBED_MODEL", "text-embedding-3-small")
	t.Setenv(EnvAPIKey, "")
	_, err := ProviderFromEnvWithNext("4) 重试 kb index rebuild --embed")
	if err == nil {
		t.Fatal("OPENAI_API_KEY 未设置应报错")
	}
	msg := err.Error()
	for _, want := range []string{"OPENAI_API_KEY", "export", DefaultOpenAIBaseURL, "kb index rebuild --embed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("key 缺失错误应含 %q: %v", want, msg)
		}
	}
}

// M6-C:KB_EMBED_MODEL 未设置(openai 提供者)= 功能整体关闭,可行动报错
// (提示语按 OpenAI 生态给,含数据出域提醒)。
func TestEmbedOpenAIModelMissingActionableError(t *testing.T) {
	t.Setenv("KB_EMBED_PROVIDER", ProviderOpenAI)
	t.Setenv("KB_EMBED_MODEL", "")
	t.Setenv(EnvAPIKey, testKey)
	_, err := ProviderFromEnvWithNext("5) 重试 kb search --hybrid")
	if err == nil {
		t.Fatal("KB_EMBED_MODEL 未设置应报错(不静默跳过)")
	}
	msg := err.Error()
	for _, want := range []string{"KB_EMBED_MODEL", "整体关闭", "OPENAI_API_KEY", "数据出域", "kb search --hybrid"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("未配置错误应含 %q: %v", want, msg)
		}
	}
}

// M6-C:超时错误文案可行动——含「超时」、端点(含主机)与检查负载(注入短 ctx)。
func TestEmbedOpenAITimeoutActionableText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1]}]}`))
	}))
	defer srv.Close()

	oa := NewOpenAI(srv.URL, testKey, "text-embedding-3-small")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := oa.Embed(ctx, []string{"x"})
	if err == nil {
		t.Fatal("超时应报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "超时") || !strings.Contains(msg, srv.URL) || !strings.Contains(msg, "负载") {
		t.Fatalf("超时错误应可行动(超时+端点主机+检查负载): %v", msg)
	}
}

// M6-C:HTTP 500 错误文案可行动——含状态码、端点、响应片段与排查方向;
// 安全红线:错误文案绝不含 OPENAI_API_KEY(服务端确实收到了正确的鉴权头)。
func TestEmbedOpenAIHTTPErrorActionableText(t *testing.T) {
	srv, _ := newOpenAIFake(t, nil) // 先用假端点钉死「鉴权头确实已发送」
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("服务端应收到的鉴权头不匹配: %q", got)
		}
		http.Error(w, `{"error":{"message":"insufficient_quota"}}`, http.StatusInternalServerError)
	})
	oa := NewOpenAI(srv.URL, testKey, "text-embedding-3-small")
	_, err := oa.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("500 应报错")
	}
	msg := err.Error()
	for _, want := range []string{"500", srv.URL, "insufficient_quota", "KB_EMBED_MODEL", "OPENAI_BASE_URL"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误应含 %q: %v", want, msg)
		}
	}
	if strings.Contains(msg, testKey) {
		t.Fatalf("安全红线:错误文案不得回显 API key: %v", msg)
	}
}

// M6-C:批量数不符响亮拒绝(服务端批量语义异常不静默截断/错位)。
func TestEmbedOpenAICountMismatchRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2 条输入只回 1 条
		_, _ = w.Write([]byte(`{"model":"m","data":[{"index":0,"embedding":[0.1]}]}`))
	}))
	defer srv.Close()

	oa := NewOpenAI(srv.URL, testKey, "m")
	_, err := oa.Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "与输入 2 条不符") {
		t.Fatalf("数量不符应报错并说明数量: %v", err)
	}
}

// M6-C:index 越界与重复下标一律拒绝——OpenAI 兼容服务不保证数组序,
// 只能信 index,故 index 本身异常时绝不静默错位。
func TestEmbedOpenAIBadIndexRejected(t *testing.T) {
	outOfRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","data":[{"index":5,"embedding":[0.1]}]}`))
	}))
	defer outOfRange.Close()
	oa := NewOpenAI(outOfRange.URL, testKey, "m")
	if _, err := oa.Embed(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("index 越界应报错: %v", err)
	}

	dup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","data":[{"index":0,"embedding":[0.1]},{"index":0,"embedding":[0.2]}]}`))
	}))
	defer dup.Close()
	oa2 := NewOpenAI(dup.URL, testKey, "m")
	if _, err := oa2.Embed(context.Background(), []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "重复 index") {
		t.Fatalf("index 重复应报错: %v", err)
	}
}

// M6-C:安全红线全路径钉死——各错误路径(HTTP 500/数量不符/超时/连接失败)
// 的错误文案都绝不含 OPENAI_API_KEY;鉴权头确实携带 key(只进请求头)。
func TestEmbedOpenAIKeyNeverEchoedInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("服务端应收到的鉴权头不匹配: %q", got)
		}
		http.Error(w, `{"error":"boom"}`, http.StatusBadGateway)
	}))
	defer srv.Close()
	oa := NewOpenAI(srv.URL, testKey, "m")
	if _, err := oa.Embed(context.Background(), []string{"a"}); err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("500 错误文案不得含 key: %v", err)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	oa2 := NewOpenAI(deadURL, testKey, "m")
	_, err := oa2.Embed(context.Background(), []string{"a"})
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("连接失败错误文案不得含 key: %v", err)
	}
	for _, want := range []string{"无法连接嵌入服务", "OPENAI_BASE_URL", "OPENAI_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("连接失败错误应含 %q: %v", want, err)
		}
	}
}

// M6-C:KB_EMBED_PROVIDER 选择——缺省/显式 ollama 走 Ollama 适配器
// (KB_EMBED_URL/KB_EMBED_MODEL 现有行为零变化);显式 openai 走 OpenAI
// 适配器(OPENAI_BASE_URL 缺省官方端点);非法取值响亮报错列出合法值。
func TestEmbedProviderSelection(t *testing.T) {
	t.Setenv("KB_EMBED_MODEL", "m")
	t.Setenv("KB_EMBED_URL", "")

	t.Run("缺省=ollama", func(t *testing.T) {
		t.Setenv("KB_EMBED_PROVIDER", "")
		e, err := ProviderFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		o, ok := e.(*Ollama)
		if !ok {
			t.Fatalf("缺省应构造 *Ollama,实际 %T", e)
		}
		if o.baseURL != DefaultURL || o.Model() != "m" {
			t.Fatalf("缺省行为应与既有 Ollama 路径一致: %q %q", o.baseURL, o.Model())
		}
	})
	t.Run("显式=ollama", func(t *testing.T) {
		t.Setenv("KB_EMBED_PROVIDER", "ollama")
		if _, err := ProviderFromEnv(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("显式=openai", func(t *testing.T) {
		t.Setenv("KB_EMBED_PROVIDER", ProviderOpenAI)
		t.Setenv(EnvAPIKey, testKey)
		t.Setenv(EnvBaseURL, "https://gw.example.com/")
		e, err := ProviderFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		oa, ok := e.(*OpenAI)
		if !ok {
			t.Fatalf("应构造 *OpenAI,实际 %T", e)
		}
		if oa.endpoint != "https://gw.example.com/v1/embeddings" {
			t.Fatalf("OPENAI_BASE_URL 应参与端点拼接(含尾斜杠归一): %q", oa.endpoint)
		}
	})
	t.Run("非法取值报错", func(t *testing.T) {
		t.Setenv("KB_EMBED_PROVIDER", "azure")
		_, err := ProviderFromEnv()
		if err == nil {
			t.Fatal("非法取值应报错")
		}
		msg := err.Error()
		for _, want := range []string{"azure", "ollama", "openai", "KB_EMBED_PROVIDER"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("非法取值错误应列出 %q: %v", want, msg)
			}
		}
	})
	t.Run("openai base 缺省官方端点", func(t *testing.T) {
		t.Setenv("KB_EMBED_PROVIDER", ProviderOpenAI)
		t.Setenv(EnvAPIKey, testKey)
		t.Setenv(EnvBaseURL, "")
		oa, err := FromEnvOpenAIWithNext("next")
		if err != nil {
			t.Fatal(err)
		}
		if oa.endpoint != "https://api.openai.com/v1/embeddings" {
			t.Fatalf("OPENAI_BASE_URL 缺省应拼官方端点: %q", oa.endpoint)
		}
	})
}
