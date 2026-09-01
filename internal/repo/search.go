package repo

import (
	"context"
	"errors"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/index"
	"github.com/imeepos/cas-kb/internal/object"
)

// SearchHit 是检索结果项:命中元信息 + 展示字段(标题/标签/正文,摘要由展示层派生)。
type SearchHit struct {
	Path  string
	Slug  string
	Addr  hash.Address
	Title string
	Tags  []string
	Body  []byte
	Score float64
}

// Search 在指定快照(缺省当前分支头)的检索索引上执行全文查询。
// 结果确定性:同一快照,同一查询,结果与顺序完全一致(ROADMAP M4)。
// 快照无索引(历史数据)时报错并指引 kb index rebuild。
func (r *Repo) Search(ctx context.Context, query, ref string) ([]SearchHit, error) {
	var snap *object.Snapshot
	if ref == "" {
		head, has, err := r.head(ctx)
		if err != nil {
			return nil, err
		}
		if !has {
			return nil, nil
		}
		s, err := r.loadSnapshot(ctx, head)
		if err != nil {
			return nil, err
		}
		snap = s
	} else {
		addr, err := r.Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		s, err := r.loadSnapshot(ctx, addr)
		if err != nil {
			return nil, err
		}
		snap = s
	}
	if snap.Index == "" {
		return nil, errors.New("repo: 该快照无检索索引,请先执行 kb index rebuild")
	}
	root, err := index.LoadRoot(ctx, r.st, snap.Index)
	if err != nil {
		return nil, err
	}
	hits, err := index.Search(ctx, r.st, root, query)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		ref, err := r.noteAt(ctx, h.Path, h.Addr)
		if err != nil {
			return nil, err
		}
		out = append(out, SearchHit{
			Path: h.Path, Slug: ref.Slug, Addr: h.Addr,
			Title: ref.Note.Meta.Title, Tags: ref.Note.Meta.Tags,
			Body: ref.Body, Score: h.Score,
		})
	}
	return out, nil
}
