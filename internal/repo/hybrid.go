package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/index"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// RRFK 是 Reciprocal Rank Fusion 的常数 k(社区通行值 60)。融合分
// score = Σ_i 1/(k + rank_i),rank 从 1 起——按任务红线为固定常数,不设旋钮。
const RRFK = 60

// HybridTopK 是每路排名参与融合的深度:BM25 与向量余弦各取前 HybridTopK 名
// (DESIGN §7.3);融合输出为两路集合的并(≤ 2×HybridTopK 条)。
const HybridTopK = 50

// queryEmbedTimeout 是查询词嵌入的单次调用时限(与嵌入服务 HTTP 超时同口径
// 30s;M6-B 红线:失败响亮报错,绝不降级为纯词法)。
const queryEmbedTimeout = 30 * time.Second

// hybrid 前置/执行失败的哨兵错误(全部响亮,绝不静默降级):
// CLI 原样呈现;serve 按 errors.Is 映射 409(DESIGN §8.5)。
var (
	// ErrHybridNoVec:快照无向量索引(snapshot.vec 为空)——需 rebuild --embed。
	ErrHybridNoVec = errors.New("hybrid: 该快照无向量索引,先 kb index rebuild --embed,或去掉 --hybrid 用词法检索")
	// ErrHybridModelMismatch:快照向量模型与当前 Embedder 不一致——换模型必须重建。
	ErrHybridModelMismatch = errors.New("hybrid: 向量索引模型不一致")
	// ErrHybridEmbedFailed:查询词嵌入失败(嵌入服务调用出错)。
	ErrHybridEmbedFailed = errors.New("hybrid: 查询词嵌入失败")
)

// SearchHybrid 在指定快照(缺省当前分支头)上执行混合检索(M6-B,DESIGN §7.3):
// BM25 词法腿与向量余弦语义腿做 RRF 融合。融合是纯函数——同一快照 + 同一向量
// 数据(同 model_id)→ 结果与顺序完全一致;可复现性边界 = 同快照 + 同 model_id
// (向量随模型版本变化,换模型必须重跑 kb index rebuild --embed)。
//
// 流程与失败语义(一律响亮报错,绝不静默降级):
//  1. 快照必须带 vec,否则 ErrHybridNoVec(指引 rebuild --embed);
//  2. 快照 vecroot 的 model 必须与 emb.Model() 一致,否则 ErrHybridModelMismatch
//     (模型换了要重建);查询向量维度同理入此项校验;
//  3. 查询词经 Embedder 嵌入(恰好 1 次调用,30s 上限),失败 ErrHybridEmbedFailed;
//  4. BM25 腿 = 既有倒排索引前 HybridTopK 名;向量腿 = 对快照 vec 分桶平扫余弦
//     前 HybridTopK 名;score = Σ 1/(RRFK+rank),输出按融合分降序,平局路径
//     升序(路径在融合表中唯一,不再需要地址兜底,与 BM25 排序规则同构)。
//
// emb 为 nil 时报错(调用方 CLI/serve 应先以 embed.FromEnv 拦截并给配置指引)。
func (r *Repo) SearchHybrid(ctx context.Context, query, ref string, emb embed.Embedder) ([]SearchHit, error) {
	if emb == nil {
		return nil, errors.New("hybrid: Embedder 未提供(嵌入服务未配置,先设置 KB_EMBED_MODEL)")
	}
	snap, has, err := r.snapshotFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil // 空库:与词法检索一致,返回无结果
	}
	if snap.Index == "" {
		return nil, errors.New("repo: 该快照无检索索引,请先执行 kb index rebuild")
	}
	if snap.Vec == "" {
		return nil, ErrHybridNoVec
	}
	vroot, err := r.LoadVecRoot(ctx, snap.Vec)
	if err != nil {
		return nil, err
	}
	if vroot.Model != emb.Model() {
		return nil, fmt.Errorf(
			"%w: 快照向量由模型 %q 生成,当前嵌入模型为 %q——模型已更换,请重跑 kb index rebuild --embed",
			ErrHybridModelMismatch, vroot.Model, emb.Model())
	}
	// 词法腿:BM25 前 HybridTopK 名(快照索引缺失/被精简的报错与词法检索同款)
	iroot, err := index.LoadRoot(ctx, r.st, snap.Index)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("repo: 该快照的检索索引已被 gc 精简(--keep-last);如需检索请基于最新快照,或调整 gc 保留策略")
		}
		return nil, err
	}
	bmHits, err := index.Search(ctx, r.st, iroot, query)
	if err != nil {
		return nil, err
	}
	if len(bmHits) > HybridTopK {
		bmHits = bmHits[:HybridTopK]
	}
	// 语义腿:查询词嵌入恰好 1 次(30s 上限),失败不降级
	ectx, cancel := context.WithTimeout(ctx, queryEmbedTimeout)
	defer cancel()
	vecs, err := emb.Embed(ectx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHybridEmbedFailed, err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("%w: 嵌入服务返回 %d 条向量,期望恰好 1 条非空向量", ErrHybridEmbedFailed, len(vecs))
	}
	if len(vecs[0]) != vroot.Dim {
		return nil, fmt.Errorf(
			"%w: 查询向量维度 %d 与快照向量维度 %d 不符(模型 %q 可能已更换)——请重跑 kb index rebuild --embed",
			ErrHybridModelMismatch, len(vecs[0]), vroot.Dim, emb.Model())
	}
	vecRanks, err := r.cosineRank(ctx, vroot, vecs[0], HybridTopK)
	if err != nil {
		return nil, err
	}
	// RRF 融合:score = Σ 1/(RRFK + rank),rank 从 1 起
	fused := make(map[string]float64, len(bmHits)+len(vecRanks))
	for rank, h := range bmHits {
		fused[h.Path] += 1.0 / float64(RRFK+rank+1)
	}
	for rank, v := range vecRanks {
		fused[v.Path] += 1.0 / float64(RRFK+rank+1)
	}
	// 地址解析:BM25 命中自带;仅向量腿命中的路径从索引文档表取(两者都源自
	// 同一快照 tree,fsck 已保证向量项路径存在于快照)
	addrOf := make(map[string]hash.Address, len(iroot.Docs))
	for _, d := range iroot.Docs {
		addrOf[d.Path] = d.Addr
	}
	for _, h := range bmHits {
		addrOf[h.Path] = h.Addr
	}
	paths := make([]string, 0, len(fused))
	for p := range fused {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		if fused[paths[i]] != fused[paths[j]] {
			return fused[paths[i]] > fused[paths[j]]
		}
		return paths[i] < paths[j]
	})
	out := make([]SearchHit, 0, len(paths))
	for _, p := range paths {
		addr, ok := addrOf[p]
		if !ok {
			return nil, fmt.Errorf("hybrid: 向量项 %q 不在检索索引文档表中(数据不一致)——请重跑 kb index rebuild", p)
		}
		nref, err := r.noteAt(ctx, p, addr)
		if err != nil {
			return nil, err
		}
		out = append(out, SearchHit{
			Path: p, Slug: nref.Slug, Addr: addr,
			Title: nref.Note.Meta.Title, Tags: nref.Note.Meta.Tags,
			Body: nref.Body, Score: fused[p],
		})
	}
	return out, nil
}

// vecRank 是向量余弦腿的一个排名项。
type vecRank struct {
	Path string
	Sim  float64
}

// cosineRank 对快照全部 vecshard 平扫(桶号升序、分片内 items 按路径升序,
// 两者均由编码保证),计算与查询向量的余弦相似,返回前 topK 名(相似降序,
// 平局路径升序)。纯读、无副作用;累加顺序固定,结果确定。
func (r *Repo) cosineRank(ctx context.Context, vroot *object.VecRoot, qvec []float32, topK int) ([]vecRank, error) {
	ranked := make([]vecRank, 0, 64)
	for bucket, addr := range vroot.Shards {
		if addr == "" {
			continue
		}
		sh, err := r.LoadVecShard(ctx, addr)
		if err != nil {
			return nil, err
		}
		if sh.Dim != vroot.Dim || sh.Model != vroot.Model {
			return nil, fmt.Errorf("hybrid: 分片 %d 的 model/dim(%q/%d)与根(%q/%d)不一致——请跑 kb fsck 排查,必要时 kb index rebuild --embed", bucket, sh.Model, sh.Dim, vroot.Model, vroot.Dim)
		}
		for _, item := range sh.Items {
			v, err := object.DecodeVecBase64(item.Vec)
			if err != nil {
				return nil, fmt.Errorf("hybrid: 解码 %s 的向量失败: %w", item.Path, err)
			}
			ranked = append(ranked, vecRank{Path: item.Path, Sim: cosine(qvec, v)})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Sim != ranked[j].Sim {
			return ranked[i].Sim > ranked[j].Sim
		}
		return ranked[i].Path < ranked[j].Path
	})
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	return ranked, nil
}

// cosine 计算两向量的余弦相似(float64 累加,顺序固定故确定);任一为零向量
// 时约定返回 0(零向量无方向,相似无意义;确定性行为钉死于测试)。
func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
