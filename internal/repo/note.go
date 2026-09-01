package repo

import (
	"context"
	"errors"
	"fmt"

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
type NoteRef struct {
	Slug string
	Addr hash.Address
	Note *object.Note
	Body []byte
}

// SetNote 写入(或覆盖)一条条目并推进分支,返回新快照与条目的地址。
func (r *Repo) SetNote(ctx context.Context, slug string, in NoteInput, msg string) (hash.Address, hash.Address, error) {
	if in.Title == "" {
		return "", "", errors.New("repo: 条目标题不能为空")
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
	t.Set(slug, noteAddr)
	snapAddr, err := r.commitTree(ctx, t, msg, hasHead)
	if err != nil {
		return "", "", err
	}
	return snapAddr, noteAddr, nil
}

// RemoveNote 删除一条条目并推进分支。
func (r *Repo) RemoveNote(ctx context.Context, slug, msg string) (hash.Address, error) {
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", err
	}
	t = t.Clone()
	t.Delete(slug)
	return r.commitTree(ctx, t, msg, hasHead)
}

// Note 读取当前快照中一条条目的详情(含正文)。
func (r *Repo) Note(ctx context.Context, slug string) (*NoteRef, error) {
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	addr, ok := t.Get(slug)
	if !ok {
		return nil, store.ErrNotFound
	}
	return r.noteAt(ctx, slug, addr)
}

// ListNotes 列出当前快照中的全部条目。
func (r *Repo) ListNotes(ctx context.Context) ([]*NoteRef, error) {
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	var out []*NoteRef
	for _, e := range t.Entries {
		ref, err := r.noteAt(ctx, e.Slug, e.Addr)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
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

// noteAt 按 slug/addr 读取条目详情。
func (r *Repo) noteAt(ctx context.Context, slug string, addr hash.Address) (*NoteRef, error) {
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
	return &NoteRef{Slug: slug, Addr: addr, Note: n, Body: body}, nil
}

// commitTree 写 tree + snapshot 并推进分支头。
func (r *Repo) commitTree(ctx context.Context, t *object.Tree, msg string, hasHead bool) (hash.Address, error) {
	if err := object.ValidateTree(t); err != nil {
		return "", err
	}
	treeData, err := object.EncodeTree(t)
	if err != nil {
		return "", err
	}
	treeAddr, err := r.st.Put(ctx, object.KindTree, treeData)
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
