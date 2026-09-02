package repo

import (
	"context"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/index"
	"github.com/imeepos/cas-kb/internal/object"
)

// collectLeaves 递归收集 root tree 下全部条目:全路径 → note 地址。
func (r *Repo) collectLeaves(ctx context.Context, t *object.Tree, prefix []string, out map[string]hash.Address) error {
	for _, e := range t.Entries {
		path := JoinPath(append(append([]string{}, prefix...), e.Slug))
		if e.Type == object.EntryDir {
			sub, err := r.loadTree(ctx, e.Addr)
			if err != nil {
				return err
			}
			if err := r.collectLeaves(ctx, sub, append(prefix, e.Slug), out); err != nil {
				return err
			}
			continue
		}
		out[path] = e.Addr
	}
	return nil
}

// updateIndex 计算并落盘新索引:oldRootAddr 为空走全量构建;
// 否则按新旧 tree 的叶子差异增量重建;结构无差异时原地址复用。
// 旧索引缺失/损坏时退化为全量重建(自愈,兼容 M4 之前的旧快照)。
func (r *Repo) updateIndex(ctx context.Context, oldRootAddr hash.Address, oldTree, newTree *object.Tree) (hash.Address, error) {
	newLeaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, newTree, nil, newLeaves); err != nil {
		return "", err
	}
	if oldRootAddr == "" {
		return r.fullIndex(ctx, newLeaves)
	}
	oldLeaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, oldTree, nil, oldLeaves); err != nil {
		return "", err
	}
	var deltas []*index.NoteDelta
	for path, newAddr := range newLeaves {
		oldAddr, ok := oldLeaves[path]
		if ok && oldAddr == newAddr {
			continue
		}
		d := &index.NoteDelta{}
		if ok {
			old, err := index.NoteTerms(ctx, r.st, oldAddr, path)
			if err != nil {
				return "", err
			}
			d.Old = old
		}
		nt, err := index.NoteTerms(ctx, r.st, newAddr, path)
		if err != nil {
			return "", err
		}
		d.New = nt
		deltas = append(deltas, d)
	}
	for path, oldAddr := range oldLeaves {
		if _, ok := newLeaves[path]; ok {
			continue
		}
		old, err := index.NoteTerms(ctx, r.st, oldAddr, path)
		if err != nil {
			return "", err
		}
		deltas = append(deltas, &index.NoteDelta{Old: old})
	}
	if len(deltas) == 0 {
		return oldRootAddr, nil
	}
	oldRoot, err := index.LoadRoot(ctx, r.st, oldRootAddr)
	if err != nil {
		return r.fullIndex(ctx, newLeaves)
	}
	return index.ApplyDelta(ctx, r.st, oldRoot, deltas)
}

// fullIndex 对叶子集合全量建索引。
func (r *Repo) fullIndex(ctx context.Context, leaves map[string]hash.Address) (hash.Address, error) {
	docs := make([]index.DocTerms, 0, len(leaves))
	for path, addr := range leaves {
		dt, err := index.NoteTerms(ctx, r.st, addr, path)
		if err != nil {
			return "", err
		}
		docs = append(docs, *dt)
	}
	return index.FullBuild(ctx, r.st, docs)
}

// RebuildIndex 从当前头快照全量重建检索索引并落一个新快照。
// tree 内容不变(结构共享),仅快照头与索引对象为新增;无头时等价首次建库。
func (r *Repo) RebuildIndex(ctx context.Context, msg string) (hash.Address, hash.Address, error) {
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", "", err
	}
	leaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, t, nil, leaves); err != nil {
		return "", "", err
	}
	rootAddr, err := r.fullIndex(ctx, leaves)
	if err != nil {
		return "", "", err
	}
	treeAddr, err := r.putTree(ctx, t)
	if err != nil {
		return "", "", err
	}
	snap := &object.Snapshot{Kind: object.KindSnapshot, Root: treeAddr, Time: r.now(), Message: msg, Index: rootAddr}
	if hasHead {
		head, _, err := r.head(ctx)
		if err != nil {
			return "", "", err
		}
		snap.Parents = []hash.Address{head}
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
	return snapAddr, rootAddr, nil
}
