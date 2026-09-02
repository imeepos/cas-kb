package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// 批量导入(压测结论的根治路径):单条 SetNote 的索引增量会把「受影响分片」
// 扩大到几乎全部分片(一篇笔记的词元散布在绝大多数桶),2000 条逐条写入
// 累计 O(N²) 字节、耗时线性退化。BulkImport 把 N 条合并为**一次提交+
// 一次索引增量**:树全程在内存构建(只落改动的目录),索引按新旧叶子
// 差异做一次批量 ApplyDelta,最终仅产生一个快照。逐条 SetNote 的提交
// 语义(逐条历史)保持不变,两者服务于不同场景。

// BulkInput 是批量导入的一条输入。
type BulkInput struct {
	Path string
	In   NoteInput
}

// memNode 是批量导入期间的内存树节点:tree 为当前(未落库)内容,
// children 追踪新建/已展开的子目录,dirty 标记是否需要落库。
type memNode struct {
	tree     *object.Tree
	children map[string]*memNode
	dirty    bool
}

// child 返回(或创建)名字为 name 的子目录节点。
// 已存在于 store 的子目录懒加载;新建子目录先给空树,地址由 finalize 回填。
func (n *memNode) child(ctx context.Context, r *Repo, name string) (*memNode, error) {
	if n.children == nil {
		n.children = map[string]*memNode{}
	}
	if c, ok := n.children[name]; ok {
		return c, nil
	}
	e, ok := n.tree.Lookup(name)
	if !ok {
		c := &memNode{tree: object.NewTree()}
		n.children[name] = c
		return c, nil
	}
	if e.Type != object.EntryDir {
		return nil, fmt.Errorf("repo: %q 是条目,不能作为目录", name)
	}
	sub, err := r.loadTree(ctx, e.Addr)
	if err != nil {
		return nil, err
	}
	c := &memNode{tree: sub}
	n.children[name] = c
	return c, nil
}

// BulkImport 一次提交写入多条笔记,返回新快照地址与导入条数。
// 语义:树全程在内存构建、自底向上只落改动目录;索引基于当前头做一次
// 批量增量(每分片只重写一次);快照仅一个(parents=[旧头])。
// 路径冲突语义与 SetNote 一致:同名目录/条目冲突报错,同路径重复出现
// 以最后一条为准。
func (r *Repo) BulkImport(ctx context.Context, items []BulkInput, msg string) (hash.Address, int, error) {
	if len(items) == 0 {
		return "", 0, errors.New("repo: 批量导入为空")
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", 0, err
	}
	root := &memNode{tree: t}
	imported := 0
	for _, it := range items {
		dirs, slug, err := SplitNotePath(it.Path)
		if err != nil {
			return "", 0, err
		}
		if it.In.Title == "" {
			return "", 0, fmt.Errorf("repo: 条目 %q 标题不能为空", it.Path)
		}
		node := root
		for _, d := range dirs {
			if node, err = node.child(ctx, r, d); err != nil {
				return "", 0, err
			}
		}
		if e, ok := node.tree.Lookup(slug); ok && e.Type == object.EntryDir {
			return "", 0, fmt.Errorf("repo: %q 是目录,不能作为条目写入", it.Path)
		}
		bodyAddr, err := r.st.Put(ctx, object.KindBlob, []byte(it.In.Body))
		if err != nil {
			return "", 0, err
		}
		n := &object.Note{
			Kind:  object.KindNote,
			Meta:  object.Meta{Title: it.In.Title, Tags: it.In.Tags, CreatedAt: ts(it.In.Time, r.now), SchemaVersion: object.SchemaVersion},
			Body:  bodyAddr,
			Links: it.In.Links,
		}
		noteData, err := object.EncodeNote(n)
		if err != nil {
			return "", 0, err
		}
		noteAddr, err := r.st.Put(ctx, object.KindNote, noteData)
		if err != nil {
			return "", 0, err
		}
		node.tree.Set(slug, object.EntryNote, noteAddr)
		node.dirty = true
		imported++
	}
	// 自底向上落树:只有改动目录产生新 tree 对象,未改动条目原地址复用
	rootAddr, changed, err := r.finalizeMem(ctx, root)
	if err != nil {
		return "", 0, err
	}
	if !changed {
		return "", imported, errors.New("repo: 批量导入未产生任何变更")
	}
	// 索引:与单条提交同一条差异管线,只是 N 条变更合并为一次增量
	var oldRootAddr hash.Address
	var oldTree *object.Tree
	if hasHead {
		head, _, err := r.head(ctx)
		if err != nil {
			return "", 0, err
		}
		prev, err := r.loadSnapshot(ctx, head)
		if err != nil {
			return "", 0, err
		}
		oldRootAddr = prev.Index
		ot, err := r.treeAtSnapshot(ctx, head)
		if err != nil {
			return "", 0, err
		}
		oldTree = ot
	}
	idxAddr, err := r.updateIndex(ctx, oldRootAddr, oldTree, root.tree)
	if err != nil {
		return "", 0, err
	}
	snap := &object.Snapshot{Kind: object.KindSnapshot, Root: rootAddr, Time: r.now(), Message: msg, Index: idxAddr}
	if hasHead {
		head, _, err := r.head(ctx)
		if err != nil {
			return "", 0, err
		}
		snap.Parents = []hash.Address{head}
	}
	snapData, err := object.EncodeSnapshot(snap)
	if err != nil {
		return "", 0, err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, snapData)
	if err != nil {
		return "", 0, err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return "", 0, r.translateBranchSetErr(err)
	}
	return snapAddr, imported, nil
}

// finalizeMem 自底向上落库改动目录,返回(新地址, 是否有改动)。
// 未改动的子树不产生新对象(原条目地址在父树中已存在,原样复用)。
func (r *Repo) finalizeMem(ctx context.Context, n *memNode) (hash.Address, bool, error) {
	for name, c := range n.children {
		addr, changed, err := r.finalizeMem(ctx, c)
		if err != nil {
			return "", false, err
		}
		if changed {
			n.tree.Set(name, object.EntryDir, addr)
			n.dirty = true
		}
	}
	if !n.dirty {
		return "", false, nil
	}
	addr, err := r.putTree(ctx, n.tree)
	if err != nil {
		return "", false, err
	}
	return addr, true, nil
}
