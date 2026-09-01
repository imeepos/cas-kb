package repo

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// transfer 从远端 src 向本地 st 复制可达对象,统计传输数。
type transfer struct {
	st   store.Store
	src  store.Store
	seen map[string]bool
	n    int
}

// copy 复制 addr 及其引用对象,只取本地缺失者并计数。
func (t *transfer) copy(ctx context.Context, addr hash.Address) error {
	key := string(addr)
	if t.seen[key] {
		return nil
	}
	t.seen[key] = true
	if has, err := t.st.Has(ctx, addr); err != nil {
		return err
	} else if has {
		data, kind, err := t.st.Get(ctx, addr)
		if err != nil {
			return err
		}
		return t.copyKids(ctx, kind, data)
	}
	data, kind, err := t.src.Get(ctx, addr)
	if err != nil {
		return fmt.Errorf("repo: 从远端读取 %s: %w", addr, err)
	}
	if _, err := t.st.Put(ctx, kind, data); err != nil {
		return err
	}
	t.n++
	return t.copyKids(ctx, kind, data)
}

// copyKids 递归复制对象的子引用。
func (t *transfer) copyKids(ctx context.Context, kind object.Kind, data []byte) error {
	kids, err := childrenOf(kind, data)
	if err != nil || len(kids) == 0 {
		return err
	}
	for _, a := range kids {
		if err := t.copy(ctx, a); err != nil {
			return err
		}
	}
	return nil
}
