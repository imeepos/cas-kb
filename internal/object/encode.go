package object

import (
	"encoding/json"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
)

// Encode 按 kind 对对象做规范编码:
// blob 原样返回字节;结构化对象输出规范 JSON。
func Encode(k Kind, v any) ([]byte, error) {
	switch k {
	case KindBlob:
		b, ok := v.([]byte)
		if !ok {
			return nil, errKind(k)
		}
		return b, nil
	case KindNote:
		return EncodeNote(v.(*Note))
	case KindTree:
		return EncodeTree(v.(*Tree))
	case KindSnapshot:
		return EncodeSnapshot(v.(*Snapshot))
	case KindIndexRoot:
		return EncodeIndexRoot(v.(*IndexRoot))
	case KindIndexShard:
		return EncodeIndexShard(v.(*IndexShard))
	default:
		return nil, errKind(k)
	}
}

// EncodeNote 编码 note:字段声明序 + links/tags 排序,保证字节稳定。
func EncodeNote(n *Note) ([]byte, error) {
	if n.Kind != KindNote {
		return nil, errKind(n.Kind)
	}
	// 深拷贝并按 slug 稳定排序 links。
	links := append([]Link(nil), n.Links...)
	sort.SliceStable(links, func(i, j int) bool { return links[i].Slug < links[j].Slug })
	// 深拷贝并按字典序稳定排序 tags。
	tags := append([]string(nil), n.Meta.Tags...)
	sort.Strings(tags)
	cp := *n
	cp.Links = links
	cp.Meta.Tags = tags
	return json.Marshal(cp)
}

// EncodeTree 编码 tree:entries 按 slug 稳定排序。
func EncodeTree(t *Tree) ([]byte, error) {
	if t.Kind != KindTree {
		return nil, errKind(t.Kind)
	}
	entries := append([]TreeEntry(nil), t.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Slug < entries[j].Slug })
	cp := *t
	cp.Entries = entries
	return json.Marshal(cp)
}

// EncodeSnapshot 编码 snapshot:父母列表按地址排序保证稳定性。
func EncodeSnapshot(s *Snapshot) ([]byte, error) {
	if s.Kind != KindSnapshot {
		return nil, errKind(s.Kind)
	}
	parents := append([]Address(nil), s.Parents...)
	sort.Slice(parents, func(i, j int) bool { return parents[i] < parents[j] })
	cp := *s
	cp.Parents = parents
	return json.Marshal(cp)
}

// EncodeIndexRoot 编码索引根:文档表按地址排序,保证字节稳定。
func EncodeIndexRoot(ir *IndexRoot) ([]byte, error) {
	if ir.Kind != KindIndexRoot {
		return nil, errKind(ir.Kind)
	}
	docs := append([]IndexDoc(nil), ir.Docs...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Addr < docs[j].Addr })
	cp := *ir
	cp.Docs = docs
	return json.Marshal(cp)
}

// EncodeIndexShard 编码索引分片:每个词元的倒排项按 note 地址排序。
func EncodeIndexShard(s *IndexShard) ([]byte, error) {
	if s.Kind != KindIndexShard {
		return nil, errKind(s.Kind)
	}
	postings := make(map[string][]IndexPosting, len(s.Postings))
	for term, list := range s.Postings {
		cp := append([]IndexPosting(nil), list...)
		sort.Slice(cp, func(i, j int) bool { return cp[i].Addr < cp[j].Addr })
		postings[term] = cp
	}
	out := *s
	out.Postings = postings
	return json.Marshal(out)
}

// HashOf 计算对象规范编码的地址,供写入前使用。
func HashOf(k Kind, v any) (Address, error) {
	enc, err := Encode(k, v)
	if err != nil {
		return "", err
	}
	return hash.Sum(enc), nil
}
