package repo

import (
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// childrenOf 返回对象内部引用的子地址。blob 无子引用。
func childrenOf(kind object.Kind, data []byte) ([]hash.Address, error) {
	switch kind {
	case object.KindBlob:
		return nil, nil
	case object.KindNote:
		n, err := object.DecodeNote(data)
		if err != nil {
			return nil, err
		}
		return []hash.Address{n.Body}, nil
	case object.KindTree:
		t, err := object.DecodeTree(data)
		if err != nil {
			return nil, err
		}
		out := make([]hash.Address, 0, len(t.Entries))
		for _, e := range t.Entries {
			out = append(out, e.Addr)
		}
		return out, nil
	case object.KindSnapshot:
		s, err := object.DecodeSnapshot(data)
		if err != nil {
			return nil, err
		}
		out := []hash.Address{s.Root}
		out = append(out, s.Parents...)
		if s.Index != "" {
			out = append(out, s.Index) // M4:索引根随快照可达(GC/pull/fsck 共用)
		}
		return out, nil
	case object.KindIndexRoot:
		ir, err := object.DecodeIndexRoot(data)
		if err != nil {
			return nil, err
		}
		out := make([]hash.Address, 0, len(ir.Shards))
		for _, a := range ir.Shards {
			if a != "" {
				out = append(out, a)
			}
		}
		return out, nil
	case object.KindIndexShard:
		is, err := object.DecodeIndexShard(data)
		if err != nil {
			return nil, err
		}
		out := []hash.Address{}
		for _, list := range is.Postings {
			for _, p := range list {
				out = append(out, p.Addr)
			}
		}
		return out, nil
	case object.KindVecRoot:
		vr, err := object.DecodeVecRoot(data)
		if err != nil {
			return nil, err
		}
		out := make([]hash.Address, 0, len(vr.Shards))
		for _, a := range vr.Shards {
			if a != "" {
				out = append(out, a)
			}
		}
		return out, nil
	case object.KindVecShard:
		// 向量项只存条目全路径(人类可读,同 note.links 的 slug 约定),
		// 不存对象地址;路径与快照的对应关系由 fsck 按快照校验。
		return nil, nil
	default:
		return nil, fmt.Errorf("repo: 未知 kind %q", kind)
	}
}
