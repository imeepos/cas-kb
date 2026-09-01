package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// NoteInput 是写入一条条目的输入。
type NoteInput struct {
	Title string
	Body  string
	Tags  []string
	Links []object.Link
	Time  int64
}

// NoteRef 是条目及其对象的视图。
// Path 是全路径(目录段 + slug,M3.8 起);Slug 保留为路径最后一段。
type NoteRef struct {
	Path string
	Slug string
	Addr hash.Address
	Note *object.Note
	Body []byte
}

// SetNote 写入(或覆盖)一条条目并推进分支,返回新快照与条目的地址。
// path 是条目全路径:"a" 表示根目录条目,"a/b/c" 表示目录 a/b 下的条目 c;
// 缺失的中间目录自动创建(copy-on-write)。目标同名是目录时拒绝。
func (r *Repo) SetNote(ctx context.Context, path string, in NoteInput, msg string) (hash.Address, hash.Address, error) {
	if in.Title == "" {
		return "", "", errors.New("repo: 条目标题不能为空")
	}
	dirs, slug, err := SplitNotePath(path)
	if err != nil {
		return "", "", err
	}
	bodyAddr, err := r.st.Put(ctx, object.KindBlob, []byte(in.Body))
	if err != nil {
		return "", "", err
	}
	n := &object.Note{
		Kind:  object.KindNote,
		Meta:  object.Meta{Title: in.Title, Tags: in.Tags, CreatedAt: ts(in.Time, r.now), SchemaVersion: object.SchemaVersion},
		Body:  bodyAddr,
		Links: in.Links,
	}
	noteData, err := object.EncodeNote(n)
	if err != nil {
		return "", "", err
	}
	noteAddr, err := r.st.Put(ctx, object.KindNote, noteData)
	if err != nil {
		return "", "", err
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", "", err
	}
	t = t.Clone()
	_, err = r.mutateAt(ctx, t, dirs, func(dir *object.Tree) error {
		if e, ok := dir.Lookup(slug); ok && e.Type == object.EntryDir {
			return fmt.Errorf("repo: %q 是目录,不能作为条目写入", JoinPath(append(dirs, slug)))
		}
		dir.Set(slug, object.EntryNote, noteAddr)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	snapAddr, err := r.commitTree(ctx, t, msg, hasHead)
	if err != nil {
		return "", "", err
	}
	return snapAddr, noteAddr, nil
}

// RemoveNote 删除一条条目并推进分支。路径不存在或指向目录时报错。
func (r *Repo) RemoveNote(ctx context.Context, path, msg string) (hash.Address, error) {
	dirs, slug, err := SplitNotePath(path)
	if err != nil {
		return "", err
	}
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", err
	}
	t = t.Clone()
	_, err = r.mutateAt(ctx, t, dirs, func(dir *object.Tree) error {
		e, ok := dir.Lookup(slug)
		if !ok {
			return fmt.Errorf("repo: 条目 %q 不存在: %w", path, store.ErrNotFound)
		}
		if e.Type == object.EntryDir {
			return fmt.Errorf("repo: %q 是目录,请用 dir rm 删除", path)
		}
		dir.Delete(slug)
		return nil
	})
	if err != nil {
		return "", err
	}
	return r.commitTree(ctx, t, msg, hasHead)
}

// Note 读取当前快照中一条条目的详情(含正文)。path 是条目全路径。
func (r *Repo) Note(ctx context.Context, path string) (*NoteRef, error) {
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	dirs, slug, err := SplitNotePath(path)
	if err != nil {
		return nil, err
	}
	e, err := r.leafEntry(ctx, t, dirs, slug)
	if err != nil {
		return nil, err
	}
	return r.noteAt(ctx, path, e.Addr)
}

// ListNotes 列出 dir 目录(递归含子目录)下的全部条目;dir 为空表示根目录。
func (r *Repo) ListNotes(ctx context.Context, dir string) ([]*NoteRef, error) {
	parts, err := ParsePath(dir)
	if err != nil {
		return nil, err
	}
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	start, err := r.walkDir(ctx, t, parts)
	if err != nil {
		return nil, err
	}
	var out []*NoteRef
	if err := r.walkNotes(ctx, start, parts, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NoteAt 读取指定快照(分支名/地址/短标识)中的条目详情。
func (r *Repo) NoteAt(ctx context.Context, path, ref string) (*NoteRef, error) {
	addr, err := r.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	t, err := r.treeAtSnapshot(ctx, addr)
	if err != nil {
		return nil, err
	}
	dirs, slug, err := SplitNotePath(path)
	if err != nil {
		return nil, err
	}
	e, err := r.leafEntry(ctx, t, dirs, slug)
	if err != nil {
		return nil, err
	}
	return r.noteAt(ctx, path, e.Addr)
}

// currentTree 读取当前分支头的 root tree;无头时返回空树。
func (r *Repo) currentTree(ctx context.Context) (*object.Tree, bool, error) {
	head, has, err := r.head(ctx)
	if err != nil {
		return nil, false, err
	}
	if !has {
		return object.NewTree(), false, nil
	}
	t, err := r.treeAtSnapshot(ctx, head)
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// walkDir 沿目录组件只读下钻,返回目标目录树。组件缺失或指向条目时报错。
func (r *Repo) walkDir(ctx context.Context, t *object.Tree, parts []string) (*object.Tree, error) {
	cur := t
	for i, name := range parts {
		e, ok := cur.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("repo: 目录 %q 不存在: %w", JoinPath(parts[:i+1]), store.ErrNotFound)
		}
		if e.Type != object.EntryDir {
			return nil, fmt.Errorf("repo: %q 是条目,不是目录", JoinPath(parts[:i+1]))
		}
		sub, err := r.loadTree(ctx, e.Addr)
		if err != nil {
			return nil, err
		}
		cur = sub
	}
	return cur, nil
}

// leafEntry 在 dirs 目录下定位叶子 slug,要求它是条目(note)。
func (r *Repo) leafEntry(ctx context.Context, t *object.Tree, dirs []string, slug string) (object.TreeEntry, error) {
	dir, err := r.walkDir(ctx, t, dirs)
	if err != nil {
		return object.TreeEntry{}, err
	}
	e, ok := dir.Lookup(slug)
	if !ok {
		return object.TreeEntry{}, fmt.Errorf("repo: 条目 %q 不存在: %w", JoinPath(append(dirs, slug)), store.ErrNotFound)
	}
	if e.Type != object.EntryNote {
		return object.TreeEntry{}, fmt.Errorf("repo: %q 是目录,不是条目", JoinPath(append(dirs, slug)))
	}
	return e, nil
}

// walkNotes 递归收集 dir 树下全部条目;输出按路径字典序(目录优先下钻)。
func (r *Repo) walkNotes(ctx context.Context, t *object.Tree, prefix []string, out *[]*NoteRef) error {
	entries := append([]object.TreeEntry(nil), t.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Slug < entries[j].Slug })
	for _, e := range entries {
		path := JoinPath(append(append([]string{}, prefix...), e.Slug))
		if e.Type == object.EntryDir {
			sub, err := r.loadTree(ctx, e.Addr)
			if err != nil {
				return err
			}
			if err := r.walkNotes(ctx, sub, append(prefix, e.Slug), out); err != nil {
				return err
			}
			continue
		}
		ref, err := r.noteAt(ctx, path, e.Addr)
		if err != nil {
			return err
		}
		*out = append(*out, ref)
	}
	return nil
}

// noteAt 按路径/addr 读取条目详情。
func (r *Repo) noteAt(ctx context.Context, path string, addr hash.Address) (*NoteRef, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	if kind != object.KindNote {
		return nil, fmt.Errorf("repo: %s 不是 note", addr)
	}
	n, err := object.DecodeNote(data)
	if err != nil {
		return nil, err
	}
	body, err := r.blobOf(ctx, n.Body)
	if err != nil {
		return nil, err
	}
	slug := path
	if i := strings.LastIndex(path, PathSep); i >= 0 {
		slug = path[i+1:]
	}
	return &NoteRef{Path: path, Slug: slug, Addr: addr, Note: n, Body: body}, nil
}

// commitTree 写 tree + snapshot 并推进分支头。
func (r *Repo) commitTree(ctx context.Context, t *object.Tree, msg string, hasHead bool) (hash.Address, error) {
	treeAddr, err := r.putTree(ctx, t)
	if err != nil {
		return "", err
	}
	snap := &object.Snapshot{Kind: object.KindSnapshot, Root: treeAddr, Time: r.now(), Message: msg}
	if hasHead {
		head, _, err := r.head(ctx)
		if err != nil {
			return "", err
		}
		snap.Parents = []hash.Address{head}
	}
	snapData, err := object.EncodeSnapshot(snap)
	if err != nil {
		return "", err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, snapData)
	if err != nil {
		return "", err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return "", err
	}
	return snapAddr, nil
}

func ts(v int64, now func() int64) int64 {
	if v > 0 {
		return v
	}
	return now()
}
