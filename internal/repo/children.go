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
		return out, nil
	default:
		return nil, fmt.Errorf("repo: 未知 kind %q", kind)
	}
}
