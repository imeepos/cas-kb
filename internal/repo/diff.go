package repo

import (
	"context"
	"fmt"
	"sort"

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

// Change 是两个快照间单个条目按全路径的差异。
// 条目跨目录移动表现为旧路径 removed + 新路径 added(地址不变)。
type Change struct {
	Path string
	Type ChangeType
	From hash.Address // added 时为 ""
	To   hash.Address // removed 时为 ""
}

// Diff 比较两个快照(分支名或地址)的条目差异(递归含子目录,按全路径)。
// 输出按路径字典序排列,结果确定可复现。
func (r *Repo) Diff(ctx context.Context, baseRef, tipRef string) ([]Change, error) {
	base, err := r.resolveSnapshot(ctx, baseRef)
	if err != nil {
		return nil, err
	}
	tip, err := r.resolveSnapshot(ctx, tipRef)
	if err != nil {
		return nil, err
	}
	bm := map[string]hash.Address{}
	if err := r.flattenNotes(ctx, base, "", bm); err != nil {
		return nil, fmt.Errorf("repo: 展开 base tree: %w", err)
	}
	tm := map[string]hash.Address{}
	if err := r.flattenNotes(ctx, tip, "", tm); err != nil {
		return nil, fmt.Errorf("repo: 展开 tip tree: %w", err)
	}
	var changes []Change
	for p, to := range tm {
		if from, ok := bm[p]; ok {
			if from != to {
				changes = append(changes, Change{Path: p, Type: ChangeUpdated, From: from, To: to})
			}
			continue
		}
		changes = append(changes, Change{Path: p, Type: ChangeAdded, To: to})
	}
	for p, from := range bm {
		if _, ok := tm[p]; ok {
			continue
		}
		changes = append(changes, Change{Path: p, Type: ChangeRemoved, From: from})
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// flattenNotes 递归把树展开为「全路径 → note 地址」映射(只收 note 条目)。
func (r *Repo) flattenNotes(ctx context.Context, t *object.Tree, prefix string, out map[string]hash.Address) error {
	for _, e := range t.Entries {
		p := e.Slug
		if prefix != "" {
			p = prefix + PathSep + e.Slug
		}
		if e.Type == object.EntryDir {
			sub, err := r.loadTree(ctx, e.Addr)
			if err != nil {
				return err
			}
			if err := r.flattenNotes(ctx, sub, p, out); err != nil {
				return err
			}
			continue
		}
		out[p] = e.Addr
	}
	return nil
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
