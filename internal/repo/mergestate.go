package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// 合并中间态(M5-B 批次,调研 docs/research/merge-design.md §2.5.2):
// pull --merge 检出冲突时全有或全无——内核不落正式提交、不动原分支指针,
// 这里补建显式中间态,交人工/上层 Agent 裁决后收束:
//
//	<branch>-merge 分支:基线快照(树 = 自动合并树,冲突条目取 ours 占位,
//	    Message 固定 "merge base",parents = [ours 头],不建索引)——形态与
//	    <branch>-stage 基线快照同构;裁决条目以 --stage 写入该分支
//	meta 键 merge.<project>.<branch>:单键 JSON(base/theirs/ours + 冲突清单),
//	    「检测存在 = 合并中」与清理原子化;可变状态仍只有命名空间小表
//
// 冻结纪律:中间态存在期间,该分支的一切直接写路径(note set/rm、dir add/rm、
// bulk import、reset、pull、index rebuild、普通 stage/commit、serve 写端点)
// 一律响亮拒绝,提示先收束或放弃;--stage 升格为裁决动作(写入 -merge 视图);
// 读操作不受限。若 ours 头在合并期间前进,continue 重放的差异会把新提交
// 静默回退(基线树落后)——冻结在语义层关死这一类竞态。

const mergeBaseMessage = "merge base"

// ErrNoMergeState 表示当前分支没有进行中的合并(收束/放弃/冻结的对照态)。
var ErrNoMergeState = errors.New("repo: 没有进行中的合并(可执行 kb pull --merge 发起三方合并)")

// MergeState 是一次进行中合并的中间态(读自 meta 键;同样的形状经
// beginMergeState 落库)。Conflicts 是发起合并时的冲突清单(契约同
// MergeConflict);Resolved/Unresolved 为展示派生(不落库):按当前 -merge
// 头逐路径归类,裁决值与 ours 占位不同即视为已裁决。
type MergeState struct {
	Base       hash.Address    `json:"base"`
	Theirs     hash.Address    `json:"theirs"`
	Ours       hash.Address    `json:"ours"`
	Conflicts  []MergeConflict `json:"conflicts,omitempty"`
	Resolved   []string        `json:"-"`
	Unresolved []string        `json:"-"`
}

// mergeBranchName 返回当前分支对应的合并中间态分支名。
func (r *Repo) mergeBranchName() string { return r.branch + "-merge" }

// mergeMetaKey 返回当前分支的合并中间态 meta 键(单键存 JSON)。
func (r *Repo) mergeMetaKey() string { return "merge." + r.project + "." + r.branch }

// mergeView 返回以 -merge 分支为作用域的仓库视图(共享 store,不建索引)。
func (r *Repo) mergeView() *Repo {
	return Open(r.st, Config{Project: r.project, Branch: r.mergeBranchName(), Now: r.now, NoIndex: true})
}

// mergingNow 报告当前分支是否存在合并中间态(轻量判定:只看 meta 键存在性,
// 不做裁决进度计算——写路径冻结守卫走这里)。
func (r *Repo) mergingNow(ctx context.Context) (bool, error) {
	_, err := r.st.MetaGet(ctx, r.mergeMetaKey())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// rejectIfMerging 在合并中间态存在时响亮拒绝写路径(冻结纪律),
// 文案始终可行动:先裁决收束或放弃。
func (r *Repo) rejectIfMerging(ctx context.Context, what string) error {
	merging, err := r.mergingNow(ctx)
	if err != nil {
		return err
	}
	if !merging {
		return nil
	}
	return fmt.Errorf("repo: %s 被拒绝:分支 %q 存在未完成合并(kb note set/rm <路径> --stage 裁决后 kb merge --continue 收束,或 kb merge --abort 放弃)", what, r.branch)
}

// MergeState 读取当前分支的合并中间态;不存在返回 (nil, nil)。
// Resolved/Unresolved 按当前 -merge 头逐冲突路径归类;中间态分支意外缺失时
// 全部记为未裁决(收束时会响亮报错,不静默)。
func (r *Repo) MergeState(ctx context.Context) (*MergeState, error) {
	v, err := r.st.MetaGet(ctx, r.mergeMetaKey())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	st := &MergeState{}
	if err := json.Unmarshal([]byte(v), st); err != nil {
		return nil, fmt.Errorf("repo: 合并中间态 meta 损坏(%s): %w", r.mergeMetaKey(), err)
	}
	st.Resolved = []string{}
	st.Unresolved = []string{}
	head, err := r.st.BranchGet(ctx, r.project, r.mergeBranchName())
	switch {
	case err == nil:
		snap, err := r.loadSnapshot(ctx, head)
		if err != nil {
			return nil, err
		}
		tree, err := r.loadTree(ctx, snap.Root)
		if err != nil {
			return nil, err
		}
		leaves := map[string]hash.Address{}
		if err := r.collectLeaves(ctx, tree, nil, leaves); err != nil {
			return nil, err
		}
		for _, c := range st.Conflicts {
			if leaves[c.Path] != c.Ours {
				st.Resolved = append(st.Resolved, c.Path)
			} else {
				st.Unresolved = append(st.Unresolved, c.Path)
			}
		}
	case errors.Is(err, store.ErrBranchNotFound):
		for _, c := range st.Conflicts {
			st.Unresolved = append(st.Unresolved, c.Path)
		}
	default:
		return nil, err
	}
	return st, nil
}

// beginMergeState 落合并中间态:合成树(冲突条目 ours 占位)写为基线快照挂到
// <branch>-merge 分支,meta 键存 base/theirs/ours 与冲突清单。仅在干净状态
// 执行一次(已有中间态时 MergeStart 先行拒绝);任一步失败重试安全,
// 最坏留下未达对象交 GC。
func (r *Repo) beginMergeState(ctx context.Context, res MergeResult) error {
	rootAddr, _, err := r.writeMerged(ctx, res.mergedNode)
	if err != nil {
		return err
	}
	base := &object.Snapshot{
		Kind:    object.KindSnapshot,
		Root:    rootAddr,
		Parents: []hash.Address{res.Ours},
		Time:    r.now(),
		Message: mergeBaseMessage,
	}
	data, err := object.EncodeSnapshot(base)
	if err != nil {
		return err
	}
	addr, err := r.st.Put(ctx, object.KindSnapshot, data)
	if err != nil {
		return err
	}
	if err := r.st.BranchSet(ctx, r.project, r.mergeBranchName(), addr); err != nil {
		return r.translateBranchSetErr(err)
	}
	payload, err := json.Marshal(MergeState{
		Base: res.Base, Theirs: res.Theirs, Ours: res.Ours, Conflicts: res.Conflicts,
	})
	if err != nil {
		return err
	}
	// meta 键是「合并中」的唯一检测信号,最后写入(先分支后 meta,
	// 中断最坏留下无信号孤儿分支,交 GC / 下次合并覆盖)
	return r.st.MetaSet(ctx, r.mergeMetaKey(), string(payload))
}

// MergeStart = 内核 Merge + 冲突中间态:复用 M5-A 判定矩阵与零冲突落库,
// 分叉检出冲突时(内核未落任何对象与指针)补建 <branch>-merge 分支与
// meta 键,冲突清单随 meta 存档(kb stage / kb merge --continue 复用)。
// 已有进行中的合并 → 响亮拒绝(先收束或放弃)。
func (r *Repo) MergeStart(ctx context.Context, src store.Store, srcProject, srcBranch string, opt MergeOptions) (MergeResult, error) {
	if err := r.rejectIfMerging(ctx, "pull --merge"); err != nil {
		return MergeResult{}, err
	}
	res, err := r.Merge(ctx, src, srcProject, srcBranch, opt)
	var mc *ErrMergeConflicts
	if errors.As(err, &mc) {
		if berr := r.beginMergeState(ctx, res); berr != nil {
			return res, fmt.Errorf("repo: 冲突中间态建立失败: %w(原始冲突: %v)", berr, err)
		}
	}
	return res, err
}

// MergeContinueResult 汇报一次合并收束。
type MergeContinueResult struct {
	Snap     hash.Address // 合并快照(parents = [ours, theirs])
	Base     hash.Address // 合并基准(LCA)
	Ours     hash.Address // 原分支头(建态时冻结)
	Theirs   hash.Address // 远端头
	Resolved int          // 应用的裁决条数
}

// MergeContinue 收束合并:重算「merge base 基线 ↔ -merge 头」的裁决差异应用到
// 自动合并树(与暂存提交同路径),索引按 ours→最终树一次批量增量,落合并快照
// (parents = [ours 头, theirs 头],theirs 自 meta 键取)推进原分支,随后清理
// 中间态(-merge 分支 + meta 键)。ours 头按冻结纪律在建态后不变,落库前
// 再校验一次(竞态兜底)。零裁决响亮拒绝——冲突条目静默保持 ours 占位等于
// 丢掉 theirs 侧变更,必须显式裁决或放弃。
func (r *Repo) MergeContinue(ctx context.Context, msg string) (MergeContinueResult, error) {
	st, err := r.MergeState(ctx)
	if err != nil {
		return MergeContinueResult{}, err
	}
	if st == nil {
		return MergeContinueResult{}, ErrNoMergeState
	}
	head, has, err := r.head(ctx)
	if err != nil {
		return MergeContinueResult{}, err
	}
	if !has || head != st.Ours {
		return MergeContinueResult{}, fmt.Errorf("repo: 原分支头 %s 与中间态记录的 ours %s 不一致,请 kb merge --abort 后重试",
			mergeShortID(head), mergeShortID(st.Ours))
	}
	mergeHead, err := r.st.BranchGet(ctx, r.project, r.mergeBranchName())
	if err != nil {
		if errors.Is(err, store.ErrBranchNotFound) {
			return MergeContinueResult{}, errors.New("repo: 中间态分支缺失,请 kb merge --abort 清理")
		}
		return MergeContinueResult{}, err
	}
	mergeSnap, err := r.loadSnapshot(ctx, mergeHead)
	if err != nil {
		return MergeContinueResult{}, err
	}
	baseSnap, err := r.mergeBaseSnapshot(ctx, mergeHead)
	if err != nil {
		return MergeContinueResult{}, err
	}
	changes, err := r.stagedChanges(ctx, baseSnap, mergeSnap)
	if err != nil {
		return MergeContinueResult{}, err
	}
	if len(changes) == 0 {
		return MergeContinueResult{}, errors.New("repo: 没有任何裁决(冲突条目仍为 ours 占位):逐条 kb note set/rm <路径> --stage 后重试;放弃合并用 kb merge --abort")
	}
	baselineTree, err := r.loadTree(ctx, baseSnap.Root)
	if err != nil {
		return MergeContinueResult{}, err
	}
	finalTree, applied, err := r.applyStagedChanges(ctx, baselineTree, changes)
	if err != nil {
		return MergeContinueResult{}, err
	}
	oursSnap, err := r.loadSnapshot(ctx, head)
	if err != nil {
		return MergeContinueResult{}, err
	}
	oursTree, err := r.loadTree(ctx, oursSnap.Root)
	if err != nil {
		return MergeContinueResult{}, err
	}
	// 索引增量:ours 快照索引 → 最终树,一次批量增量(与零冲突合并同路径)
	idxAddr, err := r.updateIndex(ctx, oursSnap.Index, oursTree, finalTree)
	if err != nil {
		return MergeContinueResult{}, err
	}
	rootAddr, err := r.putTree(ctx, finalTree)
	if err != nil {
		return MergeContinueResult{}, err
	}
	if msg == "" {
		msg = mergeDefaultMessage
	}
	snap := &object.Snapshot{
		Kind:    object.KindSnapshot,
		Root:    rootAddr,
		Parents: []hash.Address{st.Ours, st.Theirs},
		Time:    r.now(),
		Message: msg,
		Index:   idxAddr,
	}
	data, err := object.EncodeSnapshot(snap)
	if err != nil {
		return MergeContinueResult{}, err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, data)
	if err != nil {
		return MergeContinueResult{}, err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return MergeContinueResult{}, r.translateBranchSetErr(err)
	}
	// 清理中间态:先摘 meta 信号(「合并中」即刻结束),再删分支
	if err := r.st.MetaDelete(ctx, r.mergeMetaKey()); err != nil {
		return MergeContinueResult{}, err
	}
	if err := r.st.BranchDelete(ctx, r.project, r.mergeBranchName()); err != nil && !errors.Is(err, store.ErrBranchNotFound) {
		return MergeContinueResult{}, err
	}
	return MergeContinueResult{Snap: snapAddr, Base: st.Base, Ours: st.Ours, Theirs: st.Theirs, Resolved: applied}, nil
}

// MergeAbortResult 汇报一次合并放弃。
type MergeAbortResult struct {
	Resolved int // 放弃的裁决条数(供输出「放弃 N 条裁决」)
}

// MergeAbort 放弃合并:删除 <branch>-merge 分支与 meta 键,原分支指针与合并前
// 一致;中间态快照成孤儿交 GC(与 kb commit --abort 同款语义)。
func (r *Repo) MergeAbort(ctx context.Context) (MergeAbortResult, error) {
	st, err := r.MergeState(ctx)
	if err != nil {
		return MergeAbortResult{}, err
	}
	if st == nil {
		return MergeAbortResult{}, ErrNoMergeState
	}
	resolved := 0
	head, err := r.st.BranchGet(ctx, r.project, r.mergeBranchName())
	if err == nil {
		headSnap, e := r.loadSnapshot(ctx, head)
		if e == nil {
			if baseSnap, e := r.mergeBaseSnapshot(ctx, head); e == nil {
				if changes, e := r.stagedChanges(ctx, baseSnap, headSnap); e == nil {
					resolved = len(changes)
				}
			}
		}
	}
	if err := r.st.MetaDelete(ctx, r.mergeMetaKey()); err != nil {
		return MergeAbortResult{}, err
	}
	if err := r.st.BranchDelete(ctx, r.project, r.mergeBranchName()); err != nil && !errors.Is(err, store.ErrBranchNotFound) {
		return MergeAbortResult{}, err
	}
	return MergeAbortResult{Resolved: resolved}, nil
}

// mergeBaseSnapshot 沿 parents 回溯中间态基线快照(Message = merge base)。
func (r *Repo) mergeBaseSnapshot(ctx context.Context, head hash.Address) (*object.Snapshot, error) {
	cur := head
	for {
		s, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		if s.Message == mergeBaseMessage {
			return s, nil
		}
		if len(s.Parents) == 0 {
			return nil, errors.New("repo: 中间态分支缺少基线快照,请 kb merge --abort 重建")
		}
		cur = s.Parents[0]
	}
}

// applyStagedChanges 把「基线 → 暂存/裁决」差异应用到一棵树(以 baseTree 为底,
// 同名路径以变更为准覆盖,其余保留):返回新树与已应用条数。暂存提交与合并
// 收束共用(调研 §2.5.2:continue 把「基线↔-merge 头」差异应用到自动合并树)。
func (r *Repo) applyStagedChanges(ctx context.Context, baseTree *object.Tree, changes []stagedChange) (*object.Tree, int, error) {
	newTree := baseTree.Clone()
	applied := 0
	for _, ch := range changes {
		dirs, slug, err := SplitNotePath(ch.Path)
		if err != nil {
			return nil, 0, err
		}
		if ch.Removed {
			if _, werr := r.walkDir(ctx, newTree, dirs); werr != nil {
				applied++ // 目录链在基线已不存在:删除目标已不在,视为已应用
				continue
			}
			_, err = r.mutateAt(ctx, newTree, dirs, func(dir *object.Tree) error {
				if e, ok := dir.Lookup(slug); ok && e.Type == object.EntryNote {
					dir.Delete(slug)
				}
				return nil
			})
			if err != nil {
				return nil, 0, err
			}
			applied++
			continue
		}
		_, err = r.mutateAt(ctx, newTree, dirs, func(dir *object.Tree) error {
			if e, ok := dir.Lookup(slug); ok && e.Type == object.EntryDir {
				return fmt.Errorf("repo: %q 是目录,不能作为条目写入", ch.Path)
			}
			dir.Set(slug, object.EntryNote, ch.Addr)
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
		applied++
	}
	return newTree, applied, nil
}
