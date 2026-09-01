package repo

import (
	"context"

	"github.com/imeepos/cas-kb/internal/hash"
)

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
