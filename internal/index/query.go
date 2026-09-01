package index

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// BM25 参数(社区通行值):k1 控制词频饱和,b 控制文档长度归一。
const (
	BM25K1 = 1.2
	BM25B  = 0.75
)

// Hit 是一次检索命中:地址、路径与 BM25 分数。
type Hit struct {
	Addr  hash.Address
	Path  string
	Score float64
}

// Search 在索引根上执行多词 OR 查询,按 BM25 打分。
// 每个词元的贡献 = idf × tf_w×(k1+1) / (tf_w + k1×(1-b+b×dl/avgdl)),
// 其中 tf_w 为该词元的加权词频(标题3/标签2/正文1),dl 为文档加权长度。
// 确定性:同一索引根,同一查询,结果与顺序完全一致(分数降序 → 路径升序)。
func Search(ctx context.Context, st store.Store, root *object.IndexRoot, query string) ([]Hit, error) {
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	// 文档元信息:地址 → 路径与加权长度
	pathOf := make(map[string]string, len(root.Docs))
	dlOf := make(map[string]float64, len(root.Docs))
	var totalLen float64
	for _, d := range root.Docs {
		pathOf[string(d.Addr)] = d.Path
		dlOf[string(d.Addr)] = float64(d.Len)
		totalLen += float64(d.Len)
	}
	n := float64(len(root.Docs))
	avgdl := 1.0
	if n > 0 {
		avgdl = totalLen / n
	}
	// 载入查询词元所在分片(去重桶)
	shards := map[int]*object.IndexShard{}
	for _, t := range terms {
		b := BucketOf(t.Text)
		if _, ok := shards[b]; ok {
			continue
		}
		s, err := LoadShard(ctx, st, root.Shards[b])
		if err != nil {
			return nil, err
		}
		shards[b] = s
	}
	// 归并打分
	score := map[string]float64{}
	for _, t := range terms {
		shard := shards[BucketOf(t.Text)]
		if shard == nil {
			continue
		}
		postings, ok := shard.Postings[t.Text]
		if !ok {
			continue
		}
		df := float64(len(postings))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for _, p := range postings {
			tfw := float64(WeightTitle*p.Title + WeightTags*p.Tags + WeightBody*p.Body)
			if tfw <= 0 {
				continue
			}
			dl := dlOf[string(p.Addr)]
			norm := 1 - BM25B + BM25B*dl/avgdl
			score[string(p.Addr)] += idf * tfw * (BM25K1 + 1) / (tfw + BM25K1*norm)
		}
	}
	hits := make([]Hit, 0, len(score))
	for addr, s := range score {
		hits = append(hits, Hit{Addr: hash.Address(addr), Path: pathOf[addr], Score: s})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Addr < hits[j].Addr
	})
	return hits, nil
}

// String 帮助调试输出。
func (h Hit) String() string {
	return fmt.Sprintf("%s %s", h.Path, h.Addr)
}
