package repo

import (
	"context"
	"errors"

	"github.com/imeepos/cas-kb/internal/hash"
)

// ErrResetTargetNotAncestor 表示回退目标不在当前头的历史内。
var ErrResetTargetNotAncestor = errors.New("repo: 回退目标不是当前头的祖先")

// ResetResult 描述一次回退:原头、目标头与被放弃的提交数。
type ResetResult struct {
	From, To  hash.Address
	Abandoned int
}

// Reset 把当前分支指针回退到 ref(本项目可达的分支名/地址/短标识)。
// 目标必须是当前头的祖先(或当前头本身);被放弃的提交在下次 GC 前仍可经完整地址访问。
func (r *Repo) Reset(ctx context.Context, ref string) (ResetResult, error) {
	target, err := r.Resolve(ctx, ref)
	if err != nil {
		return ResetResult{}, err
	}
	old, has, err := r.head(ctx)
	if err != nil {
		return ResetResult{}, err
	}
	if !has {
		return ResetResult{}, errors.New("repo: 当前分支没有任何提交,无可回退")
	}
	abandoned, err := r.abandonedCount(ctx, old, target)
	if err != nil {
		return ResetResult{}, err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, target); err != nil {
		return ResetResult{}, err
	}
	return ResetResult{From: old, To: target, Abandoned: abandoned}, nil
}

// abandonedCount 沿 parents 从 from 走到 to,统计被放弃的提交数;
// to 不在 from 的历史内时报 ErrResetTargetNotAncestor。
func (r *Repo) abandonedCount(ctx context.Context, from, to hash.Address) (int, error) {
	if from == to {
		return 0, nil
	}
	seen := map[string]bool{string(from): true}
	cur := from
	n := 0
	for {
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return 0, err
		}
		if len(snap.Parents) == 0 {
			return 0, ErrResetTargetNotAncestor
		}
		cur = snap.Parents[0]
		if seen[string(cur)] {
			return 0, ErrResetTargetNotAncestor
		}
		seen[string(cur)] = true
		n++
		if cur == to {
			return n, nil
		}
	}
}
