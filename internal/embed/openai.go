package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// OpenAI 是 OpenAI 兼容嵌入适配器(M6-C),与 Ollama 实现同一 Embedder 接口:
// POST {OPENAI_BASE_URL}/embeddings,Authorization: Bearer {OPENAI_API_KEY},
// 请求体 {"model", "input": [texts]},响应 data[].embedding 按 index 对齐输入顺序。
//
// 端点拼接规则(注释+测试双钉死,兼容常见代理/网关两种习惯):
// 先去掉 base 尾部斜杠;若结果已以 /v1 结尾,端点 = {base}/embeddings;
// 否则端点 = {base}/v1/embeddings。例:
//   - https://host        → https://host/v1/embeddings
//   - https://host/v1     → https://host/v1/embeddings
//   - https://host/(尾斜杠) → https://host/v1/embeddings
//   - https://host/v1/    → https://host/v1/embeddings
type OpenAI struct {
	endpoint string // 按上述规则拼好的完整端点
	apiKey   string
	model    string
	client   *http.Client
	dim      int // 首次成功 Embed 后缓存的服务端实测维度
}

// DefaultOpenAIBaseURL 是未设置 OPENAI_BASE_URL 时的端点基址(官方 API)。
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// EnvAPIKey / EnvBaseURL 是 openai 提供者读取的两个环境变量(与业界
// OpenAI 兼容工具链通用约定一致,便于复用既有凭据)。
const (
	EnvAPIKey  = "OPENAI_API_KEY"
	EnvBaseURL = "OPENAI_BASE_URL"
)

// embeddingsEndpoint 按类型注释中的规则拼接端点;导出供测试钉死规则。
func embeddingsEndpoint(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/v1") {
		return b + "/embeddings"
	}
	return b + "/v1/embeddings"
}

// NewOpenAI 构造指向 baseURL 的 OpenAI 兼容适配器;apiKey 只进请求头,
// 绝不进任何错误文案或日志。
func NewOpenAI(baseURL, apiKey, model string) *OpenAI {
	return &OpenAI{
		endpoint: embeddingsEndpoint(baseURL),
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: timeout},
	}
}

// FromEnvOpenAIWithNext 按环境构造 OpenAI 兼容适配器:KB_EMBED_MODEL(必设)
// + OPENAI_API_KEY(必设,缺失=响亮报错含设置方法)+ OPENAI_BASE_URL
// (可选,默认官方端点)。next 是未配置报错文案中的下一步动作(与
// FromEnvWithNext 同一约定)。
func FromEnvOpenAIWithNext(next string) (*OpenAI, error) {
	model := os.Getenv("KB_EMBED_MODEL")
	if model == "" {
		return nil, notConfiguredOpenAIError(next)
	}
	key := os.Getenv(EnvAPIKey)
	if key == "" {
		return nil, fmt.Errorf(
			"embed: KB_EMBED_PROVIDER=openai 需要 OPENAI_API_KEY——当前未设置,嵌入功能不可用。\n"+
				"如需启用:1) 在你的 OpenAI 兼容服务方获取 API key;2) export OPENAI_API_KEY=<你的key>;\n"+
				"3) (可选)export OPENAI_BASE_URL 指定端点(默认 %s,带不带 /v1 均可);\n"+
				"%s", DefaultOpenAIBaseURL, next)
	}
	base := os.Getenv(EnvBaseURL)
	if base == "" {
		base = DefaultOpenAIBaseURL
	}
	return NewOpenAI(base, key, model), nil
}

// notConfiguredOpenAIError 返回 openai 提供者的「模型未配置」可行动错误
// (与 NotConfiguredError 同风格,提示语按 OpenAI 生态给)。
func notConfiguredOpenAIError(next string) error {
	return fmt.Errorf(
		"embed: 向量功能未配置——KB_EMBED_MODEL 未设置,嵌入功能整体关闭。\n"+
			"如需启用(KB_EMBED_PROVIDER=openai):1) export KB_EMBED_MODEL=text-embedding-3-small(或服务方支持的嵌入模型);\n"+
			"2) export OPENAI_API_KEY=<你的key>(可选 OPENAI_BASE_URL 指定端点,默认 %s);\n"+
			"3) 注意 openai 提供者会把笔记标题与正文发送到端点主机(第三方托管端点属数据出域,内网敏感库请用 ollama);\n"+
			"%s", DefaultOpenAIBaseURL, next)
}

// Model 返回模型名(向量版本化的 model_id)。
func (o *OpenAI) Model() string { return o.model }

// Dim 返回服务端实测维度;首次成功 Embed 前为 0(维度以实际返回为准)。
func (o *OpenAI) Dim() int { return o.dim }

// openAIResponse 是 /v1/embeddings 的响应体;只声明本包消费的字段。
// OpenAI 兼容服务**不保证 data 数组顺序与输入一致**,条目自带 index
// 指明对应输入下标——必须按 index 归位对齐(见 Embed)。
type openAIResponse struct {
	Model string       `json:"model"`
	Data  []openAIItem `json:"data"`
}

// openAIItem 是 data 数组单条:index = 对应输入文本下标(0 起)。
type openAIItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// Model/Dim/Embed 实现 Embedder 接口;请求体复用 Ollama 同款
// {"model","input"} 形状(embedRequest)。
var _ Embedder = (*OpenAI)(nil)

// Embed 批量嵌入文本:请求体 {"model", "input": [texts]},响应 data[]
// 按 index 归位对齐输入顺序,数量/下标不符响亮报错。所有错误文案均含
// 端点与下一步动作,且绝不含 OPENAI_API_KEY(安全红线:不日志不回显)。
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	// 请求体与 Ollama 同款形状(OpenAI 兼容契约同为 model/input 数组)
	body, err := json.Marshal(embedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: 构造请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: 构造请求失败(%s): %w", o.endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, o.wrapTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf(
			"embed: 嵌入服务 %s 返回 HTTP %d(模型 %q): %s\n"+
				"请检查:OPENAI_BASE_URL 是否正确、模型名是否为该服务支持的嵌入模型(KB_EMBED_MODEL)、"+
				"API key 是否有效或配额是否耗尽",
			o.endpoint, resp.StatusCode, o.model, strings.TrimSpace(string(snippet)))
	}
	var out openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf(
			"embed: 解析 %s 响应失败: %v\n请确认该地址是 OpenAI 兼容服务(POST …/v1/embeddings)",
			o.endpoint, err)
	}
	vecs, dim, err := alignByIndex(o.endpoint, o.model, out.Data, len(texts))
	if err != nil {
		return nil, err
	}
	o.dim = dim
	return vecs, nil
}

// alignByIndex 把响应条目按 index 归位对齐输入顺序并校验数量/下标/维度。
// OpenAI 兼容服务不保证 data 数组序,只能信 index——数量不符、下标越界、
// 重复下标、向量空、维度不一致一律响亮报错(绝不静默错位)。
func alignByIndex(endpoint, model string, items []openAIItem, want int) ([][]float32, int, error) {
	if len(items) != want {
		return nil, 0, fmt.Errorf(
			"embed: 嵌入服务 %s 返回 %d 条向量,与输入 %d 条不符(模型 %q)——"+
				"服务端批量语义异常,请确认其为 OpenAI 兼容服务",
			endpoint, len(items), want, model)
	}
	vecs := make([][]float32, want)
	dim := 0
	for _, it := range items {
		if it.Index < 0 || it.Index >= want {
			return nil, 0, fmt.Errorf(
				"embed: 嵌入服务 %s 返回越界 index %d(输入共 %d 条,模型 %q)——服务端响应异常",
				endpoint, it.Index, want, model)
		}
		if vecs[it.Index] != nil {
			return nil, 0, fmt.Errorf(
				"embed: 嵌入服务 %s 返回重复 index %d(模型 %q)——服务端响应异常",
				endpoint, it.Index, model)
		}
		if len(it.Embedding) == 0 {
			return nil, 0, fmt.Errorf(
				"embed: 嵌入服务 %s 返回第 %d 条向量为空(模型 %q)——模型可能不支持嵌入,请更换 KB_EMBED_MODEL",
				endpoint, it.Index+1, model)
		}
		if dim == 0 {
			dim = len(it.Embedding)
		} else if len(it.Embedding) != dim {
			return nil, 0, fmt.Errorf(
				"embed: 嵌入服务 %s 返回向量维度不一致(%d vs %d,模型 %q)",
				endpoint, dim, len(it.Embedding), model)
		}
		vecs[it.Index] = it.Embedding
	}
	return vecs, dim, nil
}

// wrapTransportError 把传输层失败转成可行动文案(与 Ollama 同风格);
// 错误只含端点与模型名,绝不含 API key。
func (o *OpenAI) wrapTransportError(err error) error {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Errorf(
			"embed: 嵌入服务超时(%s,上限 %s,模型 %q)——请检查服务负载或分批重试",
			o.endpoint, timeout, o.model)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"embed: 嵌入服务超时(%s,模型 %q)——请检查服务负载或分批重试",
			o.endpoint, o.model)
	}
	return fmt.Errorf(
		"embed: 无法连接嵌入服务 %s(模型 %q): %v\n"+
			"请确认 OPENAI_BASE_URL 正确且网络可达(注意代理/网关地址)、OPENAI_API_KEY/KB_EMBED_MODEL 已设置",
		o.endpoint, o.model, err)
}
