package repo

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/index"
	"github.com/imeepos/cas-kb/internal/object"
)

// VecShardCount 是语义向量索引的固定分片数,与倒排索引同为 64 桶
// (index.ShardCount);桶号 = FNV-1a(条目全路径) % VecShardCount。
// 固定片数保证同一路径永远落在同一桶,与 indexshard 同构分片;
// 路径为桶键(而非词元),因为向量项的寻址主体是笔记本身。
const VecShardCount = index.ShardCount

// embedChunk 是单次嵌入请求的批量上限:防止一次 HTTP 请求携带全库文本
// (超时上限 30s 固定,批量必须可控);路径已排序,分批确定性不受影响。
const embedChunk = 32

// VecBucketOf 返回条目全路径所属的向量分片桶号(导出供 fsck/测试定位)。
func VecBucketOf(path string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return int(h.Sum32() % VecShardCount)
}

// embedTextOf 载入一篇笔记并组装嵌入输入文本:标题 + 空行 + 正文。
// 与 BM25 的字段加权不同,嵌入模型天然联合理解标题与正文,拼接即可。
func embedTextOf(ctx context.Context, r *Repo, addr hash.Address) (string, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("vec: 读取笔记 %s: %w", addr, err)
	}
	if kind != object.KindNote {
		return "", fmt.Errorf("vec: %s 不是 note(是 %s)", addr, kind)
	}
	n, err := object.DecodeNote(data)
	if err != nil {
		return "", err
	}
	body, err := r.blobOf(ctx, n.Body)
	if err != nil {
		return "", fmt.Errorf("vec: 读取正文 %s: %w", n.Body, err)
	}
	return n.Meta.Title + "\n\n" + string(body), nil
}

// RebuildEmbeddings 从当前头快照全量重建语义向量索引并落一个新快照:
// 逐条笔记(标题+正文)调 Embedder,按 FNV-1a(路径) 桶聚合写 vecshard,
// 再写 vecroot,快照带 vec 落库。tree 内容不变(结构共享),BM25 索引
// 地址原样沿用;嵌入失败响亮中止(已写对象幂等,可安全重试)。
// 向量按 emb.Model() 版本化:换模型重建必然产出全新地址族。
func (r *Repo) RebuildEmbeddings(ctx context.Context, emb embed.Embedder, msg string) (hash.Address, hash.Address, error) {
	if emb == nil {
		return "", "", fmt.Errorf("vec: Embedder 未提供(嵌入服务未配置)")
	}
	// 冻结纪律:与 index rebuild 同规——会推进分支指针,合并中态拒绝
	if err := r.rejectIfMerging(ctx, "index rebuild --embed"); err != nil {
		return "", "", err
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", "", err
	}
	leaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, t, nil, leaves); err != nil {
		return "", "", err
	}
	paths := make([]string, 0, len(leaves))
	for p := range leaves {
		paths = append(paths, p)
	}
	sort.Strings(paths) // 确定性:路径升序逐条嵌入

	// 逐批嵌入(批量上限 embedChunk),失败即中止
	vecs := make([][]float32, len(paths))
	for start := 0; start < len(paths); start += embedChunk {
		end := start + embedChunk
		if end > len(paths) {
			end = len(paths)
		}
		texts := make([]string, 0, end-start)
		for _, p := range paths[start:end] {
			text, err := embedTextOf(ctx, r, leaves[p])
			if err != nil {
				return "", "", err
			}
			texts = append(texts, text)
		}
		batch, err := emb.Embed(ctx, texts)
		if err != nil {
			return "", "", fmt.Errorf("vec: 嵌入 %d 条笔记失败(路径 %s 起): %w", len(texts), paths[start], err)
		}
		copy(vecs[start:end], batch)
	}

	// 维度以实际返回为权威;Embedder 自报维度(如已缓存)不符则响亮拒绝
	dim := 0
	for _, v := range vecs {
		if len(v) == 0 {
			return "", "", fmt.Errorf("vec: 嵌入服务返回了空向量,拒绝落库")
		}
		if dim == 0 {
			dim = len(v)
		} else if len(v) != dim {
			return "", "", fmt.Errorf("vec: 向量维度不一致(%d vs %d),拒绝落库", dim, len(v))
		}
	}
	if d := emb.Dim(); d > 0 && d != dim {
		return "", "", fmt.Errorf("vec: Embedder 声明维度 %d 与实际返回 %d 不符", d, dim)
	}

	// 按桶聚合写 vecshard(空桶不写对象,槽位留空串)
	buckets := map[int][]object.VecItem{}
	for i, p := range paths {
		b := VecBucketOf(p)
		buckets[b] = append(buckets[b], object.VecItem{Path: p, Vec: object.EncodeVecBase64(vecs[i])})
	}
	shards := make([]hash.Address, VecShardCount)
	for b, items := range buckets {
		sh := &object.VecShard{Kind: object.KindVecShard, Model: emb.Model(), Dim: dim, Items: items}
		data, err := object.EncodeVecShard(sh)
		if err != nil {
			return "", "", err
		}
		addr, err := r.st.Put(ctx, object.KindVecShard, data)
		if err != nil {
			return "", "", err
		}
		shards[b] = addr
	}
	root := &object.VecRoot{Kind: object.KindVecRoot, Model: emb.Model(), Dim: dim, Shards: shards}
	rootData, err := object.EncodeVecRoot(root)
	if err != nil {
		return "", "", err
	}
	vecAddr, err := r.st.Put(ctx, object.KindVecRoot, rootData)
	if err != nil {
		return "", "", err
	}

	// 快照:tree 未变(地址复用),Vec 指向新向量根;BM25 索引沿用头快照
	treeAddr, err := r.putTree(ctx, t)
	if err != nil {
		return "", "", err
	}
	snap := &object.Snapshot{Kind: object.KindSnapshot, Root: treeAddr, Time: r.now(), Message: msg, Vec: vecAddr}
	if hasHead {
		head, _, err := r.head(ctx)
		if err != nil {
			return "", "", err
		}
		snap.Parents = []hash.Address{head}
		if hs, err := r.loadSnapshot(ctx, head); err == nil && hs.Index != "" {
			snap.Index = hs.Index // tree 未变,BM25 索引仍有效,地址沿用
		}
	}
	snapData, err := object.EncodeSnapshot(snap)
	if err != nil {
		return "", "", err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, snapData)
	if err != nil {
		return "", "", err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return "", "", r.translateBranchSetErr(err)
	}
	return snapAddr, vecAddr, nil
}

// LoadVecRoot 按地址载入向量索引根(kind 必须匹配)。
func (r *Repo) LoadVecRoot(ctx context.Context, addr hash.Address) (*object.VecRoot, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("vec: 读取向量根 %s: %w", addr, err)
	}
	if kind != object.KindVecRoot {
		return nil, fmt.Errorf("vec: %s 是 %s,期望 vecroot", addr, kind)
	}
	return object.DecodeVecRoot(data)
}

// LoadVecShard 按地址载入向量分片;addr 为空返回 nil(空桶)。
func (r *Repo) LoadVecShard(ctx context.Context, addr hash.Address) (*object.VecShard, error) {
	if addr == "" {
		return nil, nil
	}
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("vec: 读取向量分片 %s: %w", addr, err)
	}
	if kind != object.KindVecShard {
		return nil, fmt.Errorf("vec: %s 是 %s,期望 vecshard", addr, kind)
	}
	return object.DecodeVecShard(data)
}
