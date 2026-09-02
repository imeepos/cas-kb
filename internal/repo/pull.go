package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/store"
)

// PullResult 描述一次 pull 的结果。
type PullResult struct {
	UpToDate    bool
	Transferred int
	FastForward bool
	From, To    hash.Address
}

// ErrDiverge 表示本地与远端已分叉,需要 --force 才能覆盖。
var ErrDiverge = errors.New("repo: 本地与远端已分叉,拒绝快进")

// Pull 把远端分支的可达对象同步到本地并据祖先关系推进分支。
// srcProject 允许从同一存储的其它项目拉取(同库时零对象传输)。
func (r *Repo) Pull(ctx context.Context, src store.Store, srcProject, srcBranch string, force bool) (PullResult, error) {
	remoteHead, err := src.BranchGet(ctx, srcProject, srcBranch)
	if err != nil {
		return PullResult{}, fmt.Errorf("repo: 远端分支 %q: %w", srcBranch, err)
	}
	localHead, hasLocal, err := r.head(ctx)
	if err != nil {
		return PullResult{}, err
	}
	res := PullResult{From: localHead, To: remoteHead}
	if hasLocal && localHead == remoteHead {
		res.UpToDate = true
		return res, nil
	}
	if hasLocal && !force {
		// 调研 §1.1/§2.7 矩阵修正:远端头 ∈ 本地祖先链(本地领先)时,本地已
		// 包含远端全部内容,正确语义是「已是最新」空操作,不再要求 --force
		// (--force 仍保持覆盖回退语义,故此修正只在无旗标路径生效)。
		behind, err := r.isAncestor(ctx, localHead, remoteHead, true) // remote ∈ ancestors(local)
		if err != nil {
			return PullResult{}, err
		}
		if behind {
			res.UpToDate = true
			return res, nil
		}
	}
	tx := &transfer{st: r.st, src: src, seen: map[string]bool{}}
	if err := tx.copy(ctx, remoteHead); err != nil {
		return PullResult{}, err
	}
	res.Transferred = tx.n
	ancestor, err := r.isAncestor(ctx, remoteHead, localHead, hasLocal)
	if err != nil {
		return PullResult{}, err
	}
	if !ancestor && !force {
		return res, ErrDiverge
	}
	res.FastForward = ancestor
	if err := r.st.BranchSet(ctx, r.project, r.branch, remoteHead); err != nil {
		return PullResult{}, r.translateBranchSetErr(err)
	}
	return res, nil
}
