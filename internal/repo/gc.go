package repo

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// GCResult 汇报 GC 的标记与清扫计数。
type GCResult struct {
	Marked int
	Swept  int
}

// GC 执行标记-清扫:从全部分支头出发标记可达对象,删除其余未标记对象。
func (r *Repo) GC(ctx context.Context) (GCResult, error) {
	branches, err := r.st.BranchList(ctx)
	if err != nil {
		return GCResult{}, err
	}
	marked := map[string]bool{}
	for _, b := range branches {
		if err := r.markReachable(ctx, b.Addr, marked); err != nil {
			return GCResult{}, err
		}
	}
	res := GCResult{Marked: len(marked)}
	err = r.st.List(ctx, func(info store.ObjectInfo) error {
		if marked[string(info.Addr)] {
			return nil
		}
		if err := r.st.Delete(ctx, info.Addr); err != nil {
			return err
		}
		res.Swept++
		return nil
	})
	if err != nil {
		return GCResult{}, err
	}
	return res, nil
}

// markReachable 从 addr 出发沿引用标记全部可达对象。
func (r *Repo) markReachable(ctx context.Context, addr hash.Address, marked map[string]bool) error {
	key := string(addr)
	if marked[key] {
		return nil
	}
	marked[key] = true
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return fmt.Errorf("repo: GC 标记 %s: %w", addr, err)
	}
	var kids []hash.Address
	switch kind {
	case object.KindNote, object.KindTree, object.KindSnapshot:
		kids, err = childrenOf(kind, data)
		if err != nil {
			return fmt.Errorf("repo: GC 解析 %s(%s): %w", addr, kind, err)
		}
	}
	for _, a := range kids {
		if err := r.markReachable(ctx, a, marked); err != nil {
			return err
		}
	}
	return nil
}
