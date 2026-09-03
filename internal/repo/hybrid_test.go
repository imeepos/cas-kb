package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestHybridSearchNoVec M6-B 前置红线:快照无向量索引(snapshot.vec 为空)时
// --hybrid 响亮报错,文案含 rebuild --embed 指引与「用词法检索」出路,
// 绝不静默降级。
func TestHybridSearchNoVec(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_novec")
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel 并发", Body: "chan 语义"}, "a"); err != nil {
		t.Fatal(err)
	}
	_, err := r.SearchHybrid(ctx, "channel", "", evalEmbedder{})
	if err == nil {
		t.Fatal("快照无 vec 时 hybrid 应报错")
	}
	if !errors.Is(err, ErrHybridNoVec) {
		t.Fatalf("应为 ErrHybridNoVec 哨兵: %v", err)
	}
	for _, want := range []string{"无向量索引", "kb index rebuild --embed", "词法检索"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错应含 %q: %v", want, err)
		}
	}
}

// TestHybridSearchModelMismatch M6-B 一致性红线:快照向量模型与当前 Embedder
// 不一致(模型换了要重建)→ 响亮报错指引 kb index rebuild --embed。
func TestHybridSearchModelMismatch(t *testing.T) {
	ctx := context.Background()
	r := seedEvalCorpus(t) // 模型 eval-fake-embed
	other := &modelSwitchEmbedder{evalEmbedder{}, "other-embed-model"}
	_, err := r.SearchHybrid(ctx, "部署", "", other)
	if err == nil {
		t.Fatal("模型不一致应报错")
	}
	if !errors.Is(err, ErrHybridModelMismatch) {
		t.Fatalf("应为 ErrHybridModelMismatch 哨兵: %v", err)
	}
	for _, want := range []string{"eval-fake-embed", "other-embed-model", "kb index rebuild --embed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错应含 %q: %v", want, err)
		}
	}
}

// modelSwitchEmbedder 包装评测嵌入器改报模型名(维度/向量不变),
// 用于制造「快照向量模型 ≠ 当前 Embedder 模型」。
type modelSwitchEmbedder struct {
	evalEmbedder
	model string
}

func (m modelSwitchEmbedder) Model() string { return m.model }

// TestHybridNilEmbedder:未提供 Embedder(嵌入服务未配置)直接响亮报错,
// 不做任何检索。
func TestHybridNilEmbedder(t *testing.T) {
	ctx := context.Background()
	r := seedEvalCorpus(t)
	_, err := r.SearchHybrid(ctx, "部署", "", nil)
	if err == nil || !strings.Contains(err.Error(), "KB_EMBED_MODEL") {
		t.Fatalf("nil Embedder 应报配置错误: %v", err)
	}
}

// TestHybridEmbedFailureNoDegrade M6-B 失败语义:查询嵌入失败时响亮上抛
// (ErrHybridEmbedFailed),绝不降级为纯词法返回。
func TestHybridEmbedFailureNoDegrade(t *testing.T) {
	ctx := context.Background()
	r := seedEvalCorpus(t)
	failing := &failingEmbedder{model: "eval-fake-embed", dim: evalDims}
	hits, err := r.SearchHybrid(ctx, "部署", "", failing)
	if err == nil {
		t.Fatal("嵌入失败应报错")
	}
	if !errors.Is(err, ErrHybridEmbedFailed) {
		t.Fatalf("应为 ErrHybridEmbedFailed 哨兵: %v", err)
	}
	if hits != nil {
		t.Fatalf("失败不得返回降级结果: %+v", hits)
	}
}

// failingEmbedder 模型/维度与评测嵌入器一致但 Embed 恒失败。
type failingEmbedder struct {
	model string
	dim   int
}

func (f *failingEmbedder) Model() string { return f.model }
func (f *failingEmbedder) Dim() int      { return f.dim }
func (f *failingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("嵌入服务不可达(模拟)")
}

// TestHybridEmbedOnce M6-B 红线:查询词嵌入恰好 1 次调用(不逐条、不重试)。
func TestHybridEmbedOnce(t *testing.T) {
	ctx := context.Background()
	r := seedEvalCorpus(t)
	c := &countingEmbedder{inner: evalEmbedder{}}
	if _, err := r.SearchHybrid(ctx, "怎么把服务发布上线", "", c); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 || c.texts != 1 {
		t.Fatalf("查询嵌入应恰好 1 次调用 1 条文本,得到 calls=%d texts=%d", c.calls, c.texts)
	}
}

// countingEmbedder 计数包装(调用次数与累计文本数)。
type countingEmbedder struct {
	inner evalEmbedder
	calls int
	texts int
}

func (c *countingEmbedder) Model() string { return c.inner.Model() }
func (c *countingEmbedder) Dim() int      { return c.inner.Dim() }
func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.calls++
	c.texts += len(texts)
	return c.inner.Embed(ctx, texts)
}

// TestHybridRRFTieBreak M6-B 融合算术与平局规则钉死:查询「alpha」下词法腿
// ddd→aaa(标题+正文高词频),语义腿查询无主题词 → 零向量 → 余弦全 0 → 按
// 路径升序 aaa→ddd(零向量约定)。两腿排名互换 → 融合分相等(各 1/61+1/62),
// 平局按路径升序输出。RRF 公式 score = Σ 1/(60+rank),rank 从 1 起。
func TestHybridRRFTieBreak(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_rrf")
	seeds := []struct {
		path, title, body string
	}{
		{"tie/aaa", "语义条目", "部署,alpha 一次"},       // 词法第 2(alpha 正文 1 次)、语义第 1(路径序)
		{"tie/ddd", "alpha 词法命中", "alpha alpha"}, // 词法第 1(标题+正文)、语义第 2
	}
	for _, s := range seeds {
		if _, _, err := r.SetNote(ctx, s.path, NoteInput{Title: s.title, Body: s.body}, "seed "+s.path); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.RebuildEmbeddings(ctx, evalEmbedder{}, "embed"); err != nil {
		t.Fatal(err)
	}
	// 前置核对两条腿的排名(独立于被测代码推导期望值)
	bm, err := r.Search(ctx, "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bm) != 2 || bm[0].Path != "tie/ddd" || bm[1].Path != "tie/aaa" {
		t.Fatalf("BM25 腿排名应为 ddd→aaa,得到 %v", pathsOf(bm))
	}
	// 语义腿:查询「alpha」无主题词 → 零向量 → 余弦全 0 → 路径升序 aaa→ddd
	hy, err := r.SearchHybrid(ctx, "alpha", "", evalEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hy) != 2 {
		t.Fatalf("应融合出 2 条: %+v", hy)
	}
	want := 1.0/61 + 1.0/62 // 两条融合分相同:词法 r1+语义 r2 = 词法 r2+语义 r1
	if hy[0].Path != "tie/aaa" || hy[1].Path != "tie/ddd" {
		t.Fatalf("平局应按路径升序 aaa→ddd,得到 %v", pathsOf(hy))
	}
	for _, h := range hy {
		if diff := h.Score - want; diff < -1e-12 || diff > 1e-12 {
			t.Fatalf("%s 融合分应为 1/61+1/62=%.12f,得到 %.12f", h.Path, want, h.Score)
		}
	}
}

// TestHybridTopKDepth M6-B 融合深度:两路各取前 HybridTopK=50 名参与融合,
// 120 条全词法命中 + 全向量候选时,输出上限 = 两路并集 ≤ 100。
func TestHybridTopKDepth(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_topk")
	for i := 0; i < 120; i++ {
		path := fmt.Sprintf("bulk/n%03d", i)
		body := fmt.Sprintf("命中 编号 %d %s", i, strings.Repeat("细节", (i%7)+1))
		if _, _, err := r.SetNote(ctx, path, NoteInput{Title: fmt.Sprintf("条目 %d 命中", i), Body: body}, "seed"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.RebuildEmbeddings(ctx, evalEmbedder{}, "embed"); err != nil {
		t.Fatal(err)
	}
	bm, err := r.Search(ctx, "命中", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bm) != 120 {
		t.Fatalf("BM25 应全量命中 120 条,得到 %d", len(bm))
	}
	hy, err := r.SearchHybrid(ctx, "命中", "", evalEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hy) > 2*HybridTopK {
		t.Fatalf("融合输出应为两路前 %d 名的并集(≤%d),得到 %d", HybridTopK, 2*HybridTopK, len(hy))
	}
	// 融合分降序、非严格(RRF 分值有限集合,平局允许但顺序必须确定)
	for i := 1; i < len(hy); i++ {
		if hy[i-1].Score < hy[i].Score {
			t.Fatalf("融合输出应按分数降序:第 %d 位 %.6f < 第 %d 位 %.6f", i-1, hy[i-1].Score, i, hy[i].Score)
		}
	}
}

// TestHybridEmptyRepo:空库 hybrid 与词法检索一致,返回无结果(不报错)。
func TestHybridEmptyRepo(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_empty")
	hits, err := r.SearchHybrid(ctx, "任何", "", evalEmbedder{})
	if err != nil || hits != nil {
		t.Fatalf("空库应返回无结果,得到 hits=%v err=%v", hits, err)
	}
}

// TestHybridAtHistoricalSnapshot:--at 指向带 vec 的历史快照可检索,
// 指向无 vec 的更早快照报 ErrHybridNoVec(与词法检索的 --at 语义一致)。
func TestHybridAtHistoricalSnapshot(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_at")
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel 并发", Body: "chan 语义"}, "a"); err != nil {
		t.Fatal(err)
	}
	noVecSnap, _, err := r.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, evalEmbedder{}, "embed"); err != nil {
		t.Fatal(err)
	}
	hits, err := r.SearchHybrid(ctx, "并发", "", evalEmbedder{})
	if err != nil || len(hits) != 1 || hits[0].Path != "go/channel" {
		t.Fatalf("头快照 hybrid 应命中 go/channel: hits=%v err=%v", hits, err)
	}
	// 历史快照(带 vec)可检索
	hits2, err := r.SearchHybrid(ctx, "并发", string(noVecSnap), evalEmbedder{})
	if err == nil || !errors.Is(err, ErrHybridNoVec) {
		t.Fatalf("无 vec 历史快照应报 ErrHybridNoVec: hits=%v err=%v", hits2, err)
	}
}
