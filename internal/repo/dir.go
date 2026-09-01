package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// DirEntry 是目录的直接子项视图。
type DirEntry struct {
	Name string
	Type object.EntryType
	Addr hash.Address
}

// DirNode 是目录层级视图的一个节点(dir tree 输出用)。
type DirNode struct {
	Path     string
	Name     string
	Type     object.EntryType
	Addr     hash.Address
	Title    string // note 节点的标题;dir 节点为空
	Children []*DirNode
}

// putTree 校验、编码并写入一棵 tree,返回其地址。
func (r *Repo) putTree(ctx context.Context, t *object.Tree) (hash.Address, error) {
	if err := object.ValidateTree(t); err != nil {
		return "", err
	}
	data, err := object.EncodeTree(t)
	if err != nil {
		return "", err
	}
	return r.st.Put(ctx, object.KindTree, data)
}

// mutateAt 沿 dirs 下钻,在目标目录树上应用 mut,自底向上写新 tree
// (copy-on-write:逐层写入新子树并替换父条目地址,旧对象保持不动)。
// 缺失的中间目录自动创建;中间段命中条目(note)时拒绝。
// 传入的 root 会被就地修改,调用方须传入克隆(或新建)的树。
func (r *Repo) mutateAt(ctx context.Context, root *object.Tree, dirs []string, mut func(dir *object.Tree) error) (*object.Tree, error) {
	cur := root
	for i, name := range dirs {
		e, ok := cur.Lookup(name)
		if ok && e.Type != object.EntryDir {
			return nil, fmt.Errorf("repo: %q 是条目,不能作为目录", JoinPath(dirs[:i+1]))
		}
		var sub *object.Tree
		if ok {
			loaded, err := r.loadTree(ctx, e.Addr)
			if err != nil {
				return nil, err
			}
			sub = loaded
		} else {
			sub = object.NewTree()
		}
		newSub, err := r.mutateAt(ctx, sub, dirs[i+1:], mut)
		if err != nil {
			return nil, err
		}
		subAddr, err := r.putTree(ctx, newSub)
		if err != nil {
			return nil, err
		}
		cur.Set(name, object.EntryDir, subAddr)
		return root, nil
	}
	if err := mut(cur); err != nil {
		return nil, err
	}
	return root, nil
}

// Mkdir 创建目录(自动创建缺失的父目录,类似 mkdir -p)。
// 目录已存在时幂等返回 created=false 且不产生新快照;
// 目标或中间段是条目时报错。空目录是合法实体(空 entries 树,父链可达)。
func (r *Repo) Mkdir(ctx context.Context, path, msg string) (hash.Address, bool, error) {
	parts, err := ParsePath(path)
	if err != nil {
		return "", false, err
	}
	if len(parts) == 0 {
		return "", false, errors.New("repo: 目录路径不能为空(根目录天然存在)")
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", false, err
	}
	// 只读预检:已存在 → 幂等返回;命中条目 → 响亮失败
	exists, err := r.dirExists(ctx, t, parts)
	if err != nil {
		return "", false, err
	}
	if exists {
		return "", false, nil
	}
	t = t.Clone()
	if _, err := r.mutateAt(ctx, t, parts, func(dir *object.Tree) error { return nil }); err != nil {
		return "", false, err
	}
	snapAddr, err := r.commitTree(ctx, t, msg, hasHead)
	if err != nil {
		return "", false, err
	}
	return snapAddr, true, nil
}

// dirExists 只读判断目录是否已存在;路径中命中条目时报错。
func (r *Repo) dirExists(ctx context.Context, t *object.Tree, parts []string) (bool, error) {
	cur := t
	for i, name := range parts {
		e, ok := cur.Lookup(name)
		if !ok {
			return false, nil
		}
		if e.Type != object.EntryDir {
			return false, fmt.Errorf("repo: %q 是条目,不能作为目录", JoinPath(parts[:i+1]))
		}
		if i == len(parts)-1 {
			return true, nil
		}
		sub, err := r.loadTree(ctx, e.Addr)
		if err != nil {
			return false, err
		}
		cur = sub
	}
	return false, nil
}

// RemoveDir 删除目录。recursive=false 时仅允许删除空目录;
// recursive=true 删除整棵子树(含其中全部条目,对象留给 GC 清扫)。
func (r *Repo) RemoveDir(ctx context.Context, path, msg string, recursive bool) (hash.Address, error) {
	parts, err := ParsePath(path)
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", errors.New("repo: 根目录不能删除")
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", err
	}
	// 只读预检:父目录必须存在,目标必须是目录;非递归要求空目录。
	parent, err := r.walkDir(ctx, t, parts[:len(parts)-1])
	if err != nil {
		return "", err
	}
	last := parts[len(parts)-1]
	e, ok := parent.Lookup(last)
	if !ok {
		return "", fmt.Errorf("repo: 目录 %q 不存在", path)
	}
	if e.Type != object.EntryDir {
		return "", fmt.Errorf("repo: %q 是条目,不是目录", path)
	}
	if !recursive {
		sub, err := r.loadTree(ctx, e.Addr)
		if err != nil {
			return "", err
		}
		if len(sub.Entries) > 0 {
			return "", fmt.Errorf("repo: 目录 %q 非空,递归删除请加 --force", path)
		}
	}
	t = t.Clone()
	_, err = r.mutateAt(ctx, t, parts[:len(parts)-1], func(dir *object.Tree) error {
		dir.Delete(last)
		return nil
	})
	if err != nil {
		return "", err
	}
	return r.commitTree(ctx, t, msg, hasHead)
}

// DirLs 列出目录的直接子项(目录在前、条目在后,各自按名称排序)。
// path 为空表示根目录。
func (r *Repo) DirLs(ctx context.Context, path string) ([]DirEntry, error) {
	parts, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := r.walkDir(ctx, t, parts)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(dir.Entries))
	for _, e := range dir.Entries {
		out = append(out, DirEntry{Name: e.Slug, Type: e.Type, Addr: e.Addr})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Type == object.EntryDir) != (out[j].Type == object.EntryDir) {
			return out[i].Type == object.EntryDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// DirTree 返回 path 目录的完整层级视图(递归装载;note 节点带标题)。
// path 为空表示根目录。
func (r *Repo) DirTree(ctx context.Context, path string) (*DirNode, error) {
	parts, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := r.walkDir(ctx, t, parts)
	if err != nil {
		return nil, err
	}
	name := ""
	if len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	return r.buildNode(ctx, JoinPath(parts), name, object.EntryDir, "", dir)
}

// buildNode 递归构造层级视图节点。
func (r *Repo) buildNode(ctx context.Context, path, name string, typ object.EntryType, title string, t *object.Tree) (*DirNode, error) {
	node := &DirNode{Path: path, Name: name, Type: typ, Title: title}
	entries := append([]object.TreeEntry(nil), t.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Slug < entries[j].Slug })
	for _, e := range entries {
		childPath := e.Slug
		if path != "" {
			childPath = path + PathSep + e.Slug
		}
		if e.Type == object.EntryDir {
			sub, err := r.loadTree(ctx, e.Addr)
			if err != nil {
				return nil, err
			}
			child, err := r.buildNode(ctx, childPath, e.Slug, object.EntryDir, "", sub)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
			continue
		}
		data, _, err := r.st.Get(ctx, e.Addr)
		if err != nil {
			return nil, err
		}
		n, err := object.DecodeNote(data)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, &DirNode{
			Path: childPath, Name: e.Slug, Type: object.EntryNote,
			Addr: e.Addr, Title: n.Meta.Title,
		})
	}
	return node, nil
}
