package repo

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// ChangeType 描述单条目的变更类别。
type ChangeType string

// 三类变更。
const (
	ChangeAdded   ChangeType = "added"
	ChangeRemoved ChangeType = "removed"
	ChangeUpdated ChangeType = "updated"
)

// Change 是 tree 之间单条目的差异。
type Change struct {
	Slug string
	Type ChangeType
	From hash.Address // added 时为 ""
	To   hash.Address // removed 时为 ""
}

// Diff 比较两个快照(分支名或地址)的 root tree,输出 added/removed/updated。
func (r *Repo) Diff(ctx context.Context, baseRef, tipRef string) ([]Change, error) {
	base, err := r.resolveSnapshot(ctx, baseRef)
	if err != nil {
		return nil, err
	}
	tip, err := r.resolveSnapshot(ctx, tipRef)
	if err != nil {
		return nil, err
	}
	return compareTrees(base, tip)
}

// compareTrees 求两个 tree 的条目差异。
func compareTrees(base, tip *object.Tree) ([]Change, error) {
	bySlug := func(t *object.Tree) map[string]hash.Address {
		m := make(map[string]hash.Address, len(t.Entries))
		for _, e := range t.Entries {
			m[e.Slug] = e.Addr
		}
		return m
	}
	b := bySlug(base)
	c := bySlug(tip)
	seen := map[string]bool{}
	var changes []Change
	for slug, addr := range c {
		seen[slug] = true
		if oa, ok := b[slug]; ok {
			if oa != addr {
				changes = append(changes, Change{Slug: slug, Type: ChangeUpdated, From: oa, To: addr})
			}
		} else {
			changes = append(changes, Change{Slug: slug, Type: ChangeAdded, To: addr})
		}
	}
	for slug, addr := range b {
		if seen[slug] {
			continue
		}
		changes = append(changes, Change{Slug: slug, Type: ChangeRemoved, From: addr})
	}
	return changes, nil
}

// resolveSnapshot 把分支名或地址解析为快照地址,再读取其 tree。
func (r *Repo) resolveSnapshot(ctx context.Context, ref string) (*object.Tree, error) {
	if ref == "" {
		return object.NewTree(), nil
	}
	addr, err := r.Resolve(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("repo: 解析 %q: %w", ref, err)
	}
	return r.treeAtSnapshot(ctx, addr)
}
