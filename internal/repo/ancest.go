package repo

import (
	"context"

	"github.com/imeepos/cas-kb/internal/hash"
)

// hasCommonAncestor 报告两快照头是否存在共同祖先(祖先闭包交集非空,含相等)。
// T44 D2 文案分流用:真分叉(有共同祖先)指路 --merge,无共同历史指路
// --merge --allow-unrelated。两侧各一次 BFS,仅在 pull 分叉错误路径调用。
func (r *Repo) hasCommonAncestor(ctx context.Context, a, b hash.Address) (bool, error) {
	if a == b {
		return true, nil
	}
	seen := map[string]bool{string(a): true}
	queue := []hash.Address{a}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return false, err
		}
		for _, p := range snap.Parents {
			if !seen[string(p)] {
				seen[string(p)] = true
				queue = append(queue, p)
			}
		}
	}
	if seen[string(b)] {
		return true, nil
	}
	seenB := map[string]bool{string(b): true}
	queue = append(queue[:0], b)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return false, err
		}
		for _, p := range snap.Parents {
			if seen[string(p)] {
				return true, nil
			}
			if !seenB[string(p)] {
				seenB[string(p)] = true
				queue = append(queue, p)
			}
		}
	}
	return false, nil
}

// isAncestor 判断 localHead 是否为 remoteHead 的祖先(沿 parents 可达)。
// 本地无头(空库)视为任意远端的祖先,可快进。
func (r *Repo) isAncestor(ctx context.Context, remote, local hash.Address, hasLocal bool) (bool, error) {
	if !hasLocal {
		return true, nil
	}
	if local == remote {
		return true, nil
	}
	seen := map[string]bool{string(remote): true}
	queue := []hash.Address{remote}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return false, err
		}
		for _, p := range snap.Parents {
			if p == local {
				return true, nil
			}
			if !seen[string(p)] {
				seen[string(p)] = true
				queue = append(queue, p)
			}
		}
	}
	return false, nil
}
