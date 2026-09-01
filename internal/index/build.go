package index

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// ShardCount 是倒排索引的固定分片数;桶号 = FNV-1a(词元) % ShardCount。
// 固定片数保证同一词元永远落在同一桶,是结构共享的前提。
const ShardCount = 64

// IndexVersion 是索引根的格式版本;字段语义变更时升级。
const IndexVersion = 1

// 字段权重:标题 3、标签 2、正文 1。加权词频 tf_w = Σ w_f × tf_f,
// 加权文档长度 Len = Σ w_f × tokens_f;权重在查询期套用,调整不需重建索引。
const (
	WeightTitle = 3
	WeightTags  = 2
	WeightBody  = 1
)

// FieldTerm 是一个词元在各字段的原始词频(列表按词元字典序排列)。
type FieldTerm struct {
	Text  string
	Title int
	Tags  int
	Body  int
}

// DocTerms 是一篇笔记的分词产物:各字段词频表与加权文档长度。
type DocTerms struct {
	Addr  hash.Address
	Path  string
	Terms []FieldTerm
	Len   int
}

// NoteDelta 是一篇笔记的变更:Old 为被移除/替换的旧版本(nil=新增),
// New 为新版本(nil=删除)。
type NoteDelta struct {
	Old *DocTerms
	New *DocTerms
}

// WeightedCount 返回词元的加权词频。
func (ft FieldTerm) WeightedCount() int {
	return WeightTitle*ft.Title + WeightTags*ft.Tags + WeightBody*ft.Body
}

// BucketOf 返回词元所属分片桶号(导出供查询侧定位分片与测试)。
func BucketOf(term string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(term))
	return int(h.Sum32() % ShardCount)
}

// NoteTerms 载入一篇笔记并分词:标题/标签/正文分别计原始词频。
func NoteTerms(ctx context.Context, st store.Store, addr hash.Address, path string) (*DocTerms, error) {
	data, kind, err := st.Get(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("index: 读取笔记 %s: %w", addr, err)
	}
	if kind != object.KindNote {
		return nil, fmt.Errorf("index: %s 不是 note(是 %s)", addr, kind)
	}
	n, err := object.DecodeNote(data)
	if err != nil {
		return nil, err
	}
	bodyData, _, err := st.Get(ctx, n.Body)
	if err != nil {
		return nil, fmt.Errorf("index: 读取正文 %s: %w", n.Body, err)
	}
	type fc struct{ title, tags, body int }
	m := map[string]fc{}
	total := 0
	add := func(text string, weight, field int) {
		for _, t := range Tokenize(text) {
			v := m[t.Text]
			switch field {
			case 0:
				v.title += t.Count
			case 1:
				v.tags += t.Count
			default:
				v.body += t.Count
			}
			m[t.Text] = v
			total += t.Count * weight
		}
	}
	add(n.Meta.Title, WeightTitle, 0)
	for _, tag := range n.Meta.Tags {
		add(tag, WeightTags, 1)
	}
	add(string(bodyData), WeightBody, 2)
	terms := make([]FieldTerm, 0, len(m))
	for text, v := range m {
		terms = append(terms, FieldTerm{Text: text, Title: v.title, Tags: v.tags, Body: v.body})
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Text < terms[j].Text })
	return &DocTerms{Addr: addr, Path: path, Terms: terms, Len: total}, nil
}

// LoadRoot 按地址载入索引根。
func LoadRoot(ctx context.Context, st store.Store, addr hash.Address) (*object.IndexRoot, error) {
	data, kind, err := st.Get(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("index: 读取索引根 %s: %w", addr, err)
	}
	if kind != object.KindIndexRoot {
		return nil, fmt.Errorf("index: %s 是 %s,期望 indexroot", addr, kind)
	}
	return object.DecodeIndexRoot(data)
}

// LoadShard 按地址载入索引分片;addr 为空返回 nil(空桶)。
func LoadShard(ctx context.Context, st store.Store, addr hash.Address) (*object.IndexShard, error) {
	if addr == "" {
		return nil, nil
	}
	data, kind, err := st.Get(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("index: 读取分片 %s: %w", addr, err)
	}
	if kind != object.KindIndexShard {
		return nil, fmt.Errorf("index: %s 是 %s,期望 indexshard", addr, kind)
	}
	return object.DecodeIndexShard(data)
}

// FullBuild 从文档词频表全量构建索引:写入非空分片与索引根,返回根地址。
// 相同输入产生逐字节相同的对象(地址稳定),可安全重复执行。
func FullBuild(ctx context.Context, st store.Store, docs []DocTerms) (hash.Address, error) {
	shards := shardSet{}
	for i := range docs {
		shards.accumulate(&docs[i])
	}
	return persist(ctx, st, emptyShardAddrs(), shards, docsToRoot(docs))
}

// ApplyDelta 在旧索引根上应用一组笔记变更,只重建受影响分片:
// 受影响桶先载入旧分片再作减法/加法(同桶其他笔记的倒排项不丢);
// 未受影响桶的分片地址原样复用;重写后内容未变的桶产出相同地址,天然结构共享。
func ApplyDelta(ctx context.Context, st store.Store, oldRoot *object.IndexRoot, deltas []*NoteDelta) (hash.Address, error) {
	// 1) 受影响桶集合 = 全部 Old/New 词元所在桶
	shards := map[int]*shardAccum{}
	for _, d := range deltas {
		for _, t := range deltaTerms(d) {
			b := BucketOf(t.Text)
			if _, ok := shards[b]; !ok {
				old, err := LoadShard(ctx, st, shardAddrOf(oldRoot, b))
				if err != nil {
					return "", err
				}
				shards[b] = shardFromObject(b, old)
			}
		}
	}
	// 2) 减法(Old)+ 加法(New)
	for _, d := range deltas {
		if d.Old != nil {
			for _, t := range d.Old.Terms {
				shards[BucketOf(t.Text)].remove(t.Text, d.Old.Addr)
			}
		}
		if d.New != nil {
			for _, t := range d.New.Terms {
				shards[BucketOf(t.Text)].upsert(object.IndexPosting{
					Addr: d.New.Addr, Title: t.Title, Tags: t.Tags, Body: t.Body,
				}, t.Text)
			}
		}
	}
	// 3) 文档表:去掉全部 Old/New 地址,再补回 New 侧
	drop := map[string]bool{}
	newByID := map[string]DocTerms{}
	for _, d := range deltas {
		if d.Old != nil {
			drop[string(d.Old.Addr)] = true
		}
		if d.New != nil {
			drop[string(d.New.Addr)] = true
			newByID[string(d.New.Addr)] = *d.New
		}
	}
	docs := make([]DocTerms, 0, len(oldRoot.Docs)+len(newByID))
	for _, doc := range oldRoot.Docs {
		if drop[string(doc.Addr)] {
			continue
		}
		docs = append(docs, DocTerms{Addr: doc.Addr, Path: doc.Path, Len: doc.Len})
	}
	for _, nd := range newByID {
		docs = append(docs, nd)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Addr < docs[j].Addr })
	return persist(ctx, st, oldRoot.Shards, shards, docsToRoot(docs))
}

func deltaTerms(d *NoteDelta) []FieldTerm {
	out := []FieldTerm{}
	if d.Old != nil {
		out = append(out, d.Old.Terms...)
	}
	if d.New != nil {
		out = append(out, d.New.Terms...)
	}
	return out
}

// shardAccum 是分片的构建态。
type shardAccum struct {
	bucket   int
	postings map[string]map[string]object.IndexPosting // term -> noteAddr -> posting
}

func newShardAccum(b int) *shardAccum {
	return &shardAccum{bucket: b, postings: map[string]map[string]object.IndexPosting{}}
}

// shardSet 是分片构建态的集合,以桶号为键;出现在集合中即视为受影响桶。
type shardSet map[int]*shardAccum

// accumulate 把一篇文档的全量词频并入构建态(全量构建路径)。
func (m shardSet) accumulate(d *DocTerms) {
	for _, t := range d.Terms {
		b := BucketOf(t.Text)
		sa, ok := m[b]
		if !ok {
			sa = newShardAccum(b)
			m[b] = sa
		}
		sa.upsert(object.IndexPosting{Addr: d.Addr, Title: t.Title, Tags: t.Tags, Body: t.Body}, t.Text)
	}
}

// upsert 写入/覆盖一个倒排项。
func (sa *shardAccum) upsert(p object.IndexPosting, term string) {
	m, ok := sa.postings[term]
	if !ok {
		m = map[string]object.IndexPosting{}
		sa.postings[term] = m
	}
	m[string(p.Addr)] = p
}

// remove 删除一个词元下的一个笔记倒排项;词条清空则整个词条删除。
func (sa *shardAccum) remove(term string, addr hash.Address) {
	m, ok := sa.postings[term]
	if !ok {
		return
	}
	delete(m, string(addr))
	if len(m) == 0 {
		delete(sa.postings, term)
	}
}

// shardFromObject 把已持久化分片转为构建态(增量减法用);nil 视为空桶。
func shardFromObject(bucket int, s *object.IndexShard) *shardAccum {
	sa := newShardAccum(bucket)
	if s == nil {
		return sa
	}
	for term, list := range s.Postings {
		for _, p := range list {
			sa.upsert(p, term)
		}
	}
	return sa
}

// shardAddrOf 取旧根中指定桶的分片地址(越界/空桶返回空串)。
func shardAddrOf(root *object.IndexRoot, bucket int) hash.Address {
	if bucket < 0 || bucket >= len(root.Shards) {
		return ""
	}
	return root.Shards[bucket]
}

func emptyShardAddrs() []hash.Address {
	return make([]hash.Address, ShardCount)
}

func docsToRoot(docs []DocTerms) []object.IndexDoc {
	out := make([]object.IndexDoc, 0, len(docs))
	for _, d := range docs {
		out = append(out, object.IndexDoc{Addr: d.Addr, Path: d.Path, Len: d.Len})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

// persist 写入非空分片与索引根(地址确定,重复写幂等)。
func persist(ctx context.Context, st store.Store, oldAddrs []hash.Address, shards map[int]*shardAccum, docs []object.IndexDoc) (hash.Address, error) {
	addrs := make([]hash.Address, ShardCount)
	copy(addrs, oldAddrs)
	for b, sa := range shards {
		if sa == nil || len(sa.postings) == 0 {
			addrs[b] = "" // 受影响但已清空的桶:清槽位,不留旧地址
			continue
		}
		obj := &object.IndexShard{Kind: object.KindIndexShard, Bucket: b, Postings: map[string][]object.IndexPosting{}}
		for term, m := range sa.postings {
			list := make([]object.IndexPosting, 0, len(m))
			for _, p := range m {
				list = append(list, p)
			}
			obj.Postings[term] = list
		}
		data, err := object.EncodeIndexShard(obj)
		if err != nil {
			return "", err
		}
		addr, err := st.Put(ctx, object.KindIndexShard, data)
		if err != nil {
			return "", err
		}
		addrs[b] = addr
	}
	root := &object.IndexRoot{Kind: object.KindIndexRoot, Version: IndexVersion, Shards: addrs, Docs: docs}
	data, err := object.EncodeIndexRoot(root)
	if err != nil {
		return "", err
	}
	return st.Put(ctx, object.KindIndexRoot, data)
}
