package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// ErrAmbiguousSlug 表示链接 slug 按叶子名匹配命中多个条目,无法唯一解析。
// 文案不含 repo: 前缀——它总是被包装后输出,避免前缀重复。
var ErrAmbiguousSlug = errors.New("命中多个条目")

// ResolveLink 在当前快照中解析链接 slug,返回目标条目。
// 解析规则(DESIGN §3.3):先全路径精确匹配;未命中再按叶子名全库唯一匹配;
// 命中多个即报歧义并列出候选。解析只依赖当前快照,随版本自洽。
func (r *Repo) ResolveLink(ctx context.Context, slug string) (*NoteRef, error) {
	t, _, err := r.currentTree(ctx)
	if err != nil {
		return nil, err
	}
	return r.resolveLinkAtTree(ctx, t, slug)
}

// ResolveLinkAt 在指定快照(分支名/地址/短标识)中解析链接 slug;
// 任何历史快照内,链接解析到该版本的对象(时间旅行一致性)。
func (r *Repo) ResolveLinkAt(ctx context.Context, slug, ref string) (*NoteRef, error) {
	addr, err := r.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	t, err := r.treeAtSnapshot(ctx, addr)
	if err != nil {
		return nil, err
	}
	return r.resolveLinkAtTree(ctx, t, slug)
}

// resolveLinkAtTree 按规则解析:1) 全路径精确匹配(命中目录时报错,
// 链接只能指向条目);2) 未命中按叶子名全库唯一匹配;3) 多命中报歧义。
func (r *Repo) resolveLinkAtTree(ctx context.Context, t *object.Tree, slug string) (*NoteRef, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, errors.New("repo: 链接 slug 不能为空")
	}
	dirs, leaf, err := SplitNotePath(slug)
	if err != nil {
		return nil, err
	}
	// 1) 全路径精确匹配:目录链缺失按未命中处理,回退叶名匹配
	if dir, werr := r.walkDir(ctx, t, dirs); werr == nil {
		if e, ok := dir.Lookup(leaf); ok {
			if e.Type != object.EntryNote {
				return nil, fmt.Errorf("repo: 链接 %q 命中目录,链接只能指向条目", slug)
			}
			return r.noteAt(ctx, slug, e.Addr)
		}
	}
	// 2) 叶子名全库匹配
	var all []*NoteRef
	if err := r.walkNotes(ctx, t, nil, &all); err != nil {
		return nil, err
	}
	var cands []*NoteRef
	for _, ref := range all {
		if ref.Slug == slug {
			cands = append(cands, ref)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Path < cands[j].Path })
	switch len(cands) {
	case 1:
		return cands[0], nil
	case 0:
		return nil, fmt.Errorf("repo: 链接 %q 无解析目标: %w", slug, store.ErrNotFound)
	default:
		paths := make([]string, 0, len(cands))
		for _, c := range cands {
			paths = append(paths, c.Path)
		}
		return nil, fmt.Errorf("repo: 链接 %q %w: 候选 %s", slug, ErrAmbiguousSlug, strings.Join(paths, ", "))
	}
}
