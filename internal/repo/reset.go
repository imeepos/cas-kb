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
	if err := r.rejectIfMerging(ctx, "reset"); err != nil {
		return ResetResult{}, err
	}
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
		return ResetResult{}, r.translateBranchSetErr(err)
	}
	return ResetResult{From: old, To: target, Abandoned: abandoned}, nil
}

// abandonedCount 统计回退放弃的提交数:「from 可达而 to 不可达」的快照数。
// 沿全部 parents BFS(M5 起 merge 快照含两个 parents,first-parent 链走不到
// 的 theirs 侧历史同样计入放弃集);to 不在 from 的历史内报 ErrResetTargetNotAncestor。
// 线性历史上与旧的 first-parent 计数完全一致(|reach(from)|−|reach(to)| = 链上步数)。
func (r *Repo) abandonedCount(ctx context.Context, from, to hash.Address) (int, error) {
	if from == to {
		return 0, nil
	}
	reachFrom := map[string]bool{}
	if err := r.collectReachableFrom(ctx, from, reachFrom); err != nil {
		return 0, err
	}
	if !reachFrom[string(to)] {
		return 0, ErrResetTargetNotAncestor
	}
	reachTo := map[string]bool{}
	if err := r.collectReachableFrom(ctx, to, reachTo); err != nil {
		return 0, err
	}
	return len(reachFrom) - len(reachTo), nil
}

// collectReachableFrom 从 start 沿全部 parents BFS,把可达快照地址并入 seen。
func (r *Repo) collectReachableFrom(ctx context.Context, start hash.Address, seen map[string]bool) error {
	if seen[string(start)] {
		return nil
	}
	seen[string(start)] = true
	queue := []hash.Address{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return err
		}
		for _, p := range snap.Parents {
			if seen[string(p)] {
				continue
			}
			seen[string(p)] = true
			queue = append(queue, p)
		}
	}
	return nil
}
