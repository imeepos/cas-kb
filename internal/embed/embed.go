// Package embed 提供语义向量嵌入适配器(M6-A,DESIGN §7.3)。
//
// 四条红线(负责人裁决,本包不得越界):
//  1. 不内嵌模型运行时——嵌入一律走外挂 HTTP 服务(Ollama /api/embed);
//  2. 不引入向量数据库——向量按内容寻址对象入库(vecshard/vecroot);
//  3. 向量按 model_id 版本化入内容——model/dim 写进对象内容,跨模型必不同址;
//  4. 本批次不做检索集成——默认检索仍是 BM25,语义检索面属 M6-B。
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
	"time"
)

// Embedder 是嵌入器契约:Model/Dim 描述向量版本(入内容寻址),
// Embed 批量把文本转为向量。实现必须保证:同一文本跨调用结果确定
// (同一模型),返回向量数与入参文本数相等且顺序一致。
type Embedder interface {
	// Model 返回模型名(向量版本化的 model_id)。
	Model() string
	// Dim 返回向量维度;Ollama 实现在首次成功 Embed 前返回 0
	// (维度以服务端返回为准,构建侧以实际向量为权威)。
	Dim() int
	// Embed 批量嵌入;texts 为空返回空切片。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Ollama 适配器端点与字段核实结论(2026-09-03 实测核对官方文档
// github.com/ollama/ollama docs/api.md,main@b79067b0db7417f20108363bc22adb97f35c966a,
// 「Generate Embeddings」节;本沙箱 raw.githubusercontent 不可达,经
// api.github.com contents API 取原文逐字段核对):
//   - 端点:POST /api/embed(注意不是旧版单数 /api/embeddings——旧端点
//     只收 prompt 单串,已弃用,勿混用);
//   - 请求字段:model(模型名)、input(单个字符串或字符串数组);
//   - 响应字段:embeddings 为「数组的数组」——input 传数组时,每个输入串
//     恰好对应一个向量数组,顺序与输入一致(文档 Multiple input 示例钉死:
//     两条输入 → embeddings 两行);
//   - 其余响应字段(total_duration/load_duration/prompt_eval_count)与本包无关;
//   - 高级参数 truncate(默认 true,超出上下文截断)/options/keep_alive/
//     dimensions 本包不传,保持默认。
//
// 本实现据此只依赖 model/input/embeddings 三个字段及其批量顺序语义。
type Ollama struct {
	baseURL string
	model   string
	client  *http.Client
	dim     int // 首次成功 Embed 后缓存的服务端实测维度
}

// DefaultURL 是未设置 KB_EMBED_URL 时的嵌入服务地址。
const DefaultURL = "http://127.0.0.1:11434"

// timeout 是单次 HTTP 请求超时(任务红线:30s)。
const timeout = 30 * time.Second

// NewOllama 构造指向 baseURL 的 Ollama 适配器。
func NewOllama(baseURL, model string) *Ollama {
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: timeout},
	}
}

// FromEnv 按环境构造 Ollama 适配器:KB_EMBED_URL(默认 http://127.0.0.1:11434)
// + KB_EMBED_MODEL。KB_EMBED_MODEL 未设置 = 向量功能整体关闭——返回可行动
// 报错(绝不静默跳过),由入口原样呈现给用户。
func FromEnv() (*Ollama, error) {
	return FromEnvWithNext("4) 重试 kb index rebuild --embed")
}

// NextHybridSearch 是检索面(kb search --hybrid / mode=hybrid)未配置嵌入
// 服务时的下一步指引,CLI 与 serve 共用一份文案(M6-B)。
const NextHybridSearch = "4) 对已有库重跑 kb index rebuild --embed 生成向量;5) 重试 kb search --hybrid(或去掉 --hybrid/不传 mode=hybrid,用缺省词法检索)"

// FromEnvWithNext 同 FromEnv,next 是未配置报错文案中的下一步动作——按调用
// 场景给指引(重建向量:rebuild --embed;混合检索:search --hybrid)。
func FromEnvWithNext(next string) (*Ollama, error) {
	model := os.Getenv("KB_EMBED_MODEL")
	if model == "" {
		return nil, NotConfiguredError(next)
	}
	url := os.Getenv("KB_EMBED_URL")
	if url == "" {
		url = DefaultURL
	}
	return NewOllama(url, model), nil
}

// NotConfiguredError 返回「向量功能未配置」的可行动错误(next 为下一步指引,
// 见 NextHybridSearch)。导出供 serve 的 mode=hybrid 在未持有 Embedder 时
// 生成与 CLI 同款文案(M6-B:错误语义两条出口一致)。
func NotConfiguredError(next string) error {
	return fmt.Errorf(
		"embed: 向量功能未配置——KB_EMBED_MODEL 未设置,嵌入功能整体关闭。\n"+
			"如需启用:1) 启动嵌入服务(如 ollama serve);2) 拉取嵌入模型(如 ollama pull nomic-embed-text);\n"+
			"3) export KB_EMBED_MODEL=nomic-embed-text(可选 KB_EMBED_URL 指定服务地址,默认 %s);\n"+
			"%s", DefaultURL, next)
}

// Model 返回模型名。
func (o *Ollama) Model() string { return o.model }

// Dim 返回服务端实测维度;首次成功 Embed 前为 0(维度以实际返回为准)。
func (o *Ollama) Dim() int { return o.dim }

// embedRequest 是 /api/embed 的请求体(字段名经官方文档核实,见类型注释)。
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse 是 /api/embed 的响应体;只声明本包消费的字段。
type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed 批量嵌入文本,向量顺序与输入一致(服务契约,见 Ollama 注释)。
// 所有错误文案均含服务地址/模型名与下一步动作,可直接面向用户。
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	body, err := json.Marshal(embedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: 构造请求失败: %w", err)
	}
	url := o.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: 构造请求失败(%s): %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, o.wrapTransportError(url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf(
			"embed: 嵌入服务 %s 返回 HTTP %d(模型 %q): %s\n"+
				"请检查:模型是否已拉取(ollama pull %s)、KB_EMBED_MODEL/KB_EMBED_URL 是否正确",
			url, resp.StatusCode, o.model, strings.TrimSpace(string(snippet)), o.model)
	}
	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf(
			"embed: 解析 %s 响应失败: %v\n请确认该地址是 Ollama 兼容服务(POST /api/embed)",
			url, err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf(
			"embed: 嵌入服务 %s 返回 %d 条向量,与输入 %d 条不符(模型 %q)——"+
				"服务端批量语义异常,请确认其为 Ollama 兼容服务",
			url, len(out.Embeddings), len(texts), o.model)
	}
	dim := 0
	for i, row := range out.Embeddings {
		if len(row) == 0 {
			return nil, fmt.Errorf(
				"embed: 嵌入服务 %s 返回第 %d 条向量为空(模型 %q)——模型可能不支持嵌入,请更换 KB_EMBED_MODEL",
				url, i+1, o.model)
		}
		if dim == 0 {
			dim = len(row)
		} else if len(row) != dim {
			return nil, fmt.Errorf(
				"embed: 嵌入服务 %s 返回向量维度不一致(%d vs %d,模型 %q)",
				url, dim, len(row), o.model)
		}
	}
	o.dim = dim
	return out.Embeddings, nil
}

// wrapTransportError 把传输层失败转成可行动文案:
// 超时给「30s 超时+检查服务负载」;连接失败给「确认服务已启动」。
func (o *Ollama) wrapTransportError(url string, err error) error {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Errorf(
			"embed: 嵌入服务超时(%s,上限 %s,模型 %q)——请检查服务负载或分批重试",
			url, timeout, o.model)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"embed: 嵌入服务超时(%s,模型 %q)——请检查服务负载或分批重试",
			url, o.model)
	}
	return fmt.Errorf(
		"embed: 无法连接嵌入服务 %s(模型 %q): %v\n"+
			"请确认服务已启动(如 ollama serve)且 KB_EMBED_URL/KB_EMBED_MODEL 配置正确",
		url, o.model, err)
}
