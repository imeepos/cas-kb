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

// ErrDivergeNoCommonHistory 表示分叉且两库无共同历史(两库各自 init 即此态)。
// T44 D2(docs/review/drill-multi-cli.md):与真分叉(ErrDiverge)文案分流——
// 有共同祖先才指路 --merge;这里指引 --force 覆盖或 --merge --allow-unrelated
// 做空基线合并,修复「分叉指引改用 --merge 而 --merge 又拒绝」的指引断裂。
var ErrDivergeNoCommonHistory = errors.New("repo: 两库无共同历史,拒绝快进(--force 覆盖,或 --merge --allow-unrelated 做空基线合并)")

// Pull 把远端分支的可达对象同步到本地并据祖先关系推进分支。
// srcProject 允许从同一存储的其它项目拉取(同库时零对象传输)。
func (r *Repo) Pull(ctx context.Context, src store.Store, srcProject, srcBranch string, force bool) (PullResult, error) {
	// 冻结纪律:合并中态下 pull 会推进原分支指针(ours 头),使 continue 的
	// 裁决重放失去前提——无论 fast-forward 还是 --force 一律响亮拒绝
	if err := r.rejectIfMerging(ctx, "pull"); err != nil {
		return PullResult{}, err
	}
	remoteHead, err := src.BranchGet(ctx, srcProject, srcBranch)
	if err != nil {
		// D1(T44):远端项目存在但分支不存在(零提交)→ 视为远端无新内容,
		// 「已是最新」空操作,与「本地空拉非空可 ff」的对称语义对齐;本地分支
		// 也不存在(双空)同此路径。远端项目本身不存在仍响亮报错(防误配静默)。
		if errors.Is(err, store.ErrBranchNotFound) {
			if _, perr := src.ProjectGet(ctx, srcProject); perr == nil {
				return PullResult{UpToDate: true}, nil
			}
		}
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
		// T44 D2:分叉判定拆两类——有共同祖先的真分叉保留 ErrDiverge
		// (CLI 追加 --merge 指引);无共同历史走新文案,指路不断裂
		common, cerr := r.hasCommonAncestor(ctx, localHead, remoteHead)
		if cerr != nil {
			return res, cerr
		}
		if !common {
			return res, ErrDivergeNoCommonHistory
		}
		return res, ErrDiverge
	}
	res.FastForward = ancestor
	if err := r.st.BranchSet(ctx, r.project, r.branch, remoteHead); err != nil {
		return PullResult{}, r.translateBranchSetErr(err)
	}
	return res, nil
}
