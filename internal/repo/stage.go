package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// 暂存工作流(stage→commit,借鉴 git):
//   kb note set/rm、dir rm --stage → 变更进入 <branch>-stage 暂存分支
//   (暂存快照不建索引,单条成本恒定;检索走正式分支,暂存内容不可见)
//   kb commit        → 一次算「基线↔暂存」差异:main 单快照 + 一次索引增量,stage 归零
//   kb commit --abort → 丢弃暂存(stage 分支删除,暂存快照成为孤儿,gc 清理)
// 差异基准是**暂存基线快照**(进入暂存时 main 的树,Message 固定 "stage base"):
// base↔stage 的差异才是用户暂存的变更集;main 期间的前进不受影响(同名路径
// 以暂存为准覆盖,其余保留;无三方合并)。暂存分支仅为本机工作流单元。

const stageBaseMessage = "stage base"

// ErrNoStagedChanges 表示暂存分支不存在或暂存内容与基线无差异。
var ErrNoStagedChanges = errors.New("repo: 没有暂存内容")

// StageChange 是一条暂存差异:Op 为 A(新增)/M(更新)/D(删除)。
type StageChange struct {
	Op   string
	Path string
}

// stageBranchName 返回当前分支对应的暂存分支名。
func (r *Repo) stageBranchName() string { return r.branch + "-stage" }

// stageView 返回以暂存分支为作用域的仓库视图(共享 store,不建索引)。
func (r *Repo) stageView() *Repo {
	return Open(r.st, Config{Project: r.project, Branch: r.stageBranchName(), Now: r.now, NoIndex: true})
}

// ensureStageBase 保证暂存分支存在:不存在时以当前分支的树为基线落一个
// 不带索引的基线快照(Message=stage base;后续暂存变更以快照形式叠加其上)。
func (r *Repo) ensureStageBase(ctx context.Context) error {
	if _, err := r.st.BranchGet(ctx, r.project, r.stageBranchName()); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrBranchNotFound) {
		return err
	}
	base := &object.Snapshot{Kind: object.KindSnapshot, Time: r.now(), Message: stageBaseMessage}
	if head, has, err := r.head(ctx); err != nil {
		return err
	} else if has {
		prev, err := r.loadSnapshot(ctx, head)
		if err != nil {
			return err
		}
		base.Root = prev.Root
		base.Parents = []hash.Address{head}
	}
	if base.Root == "" {
		addr, err := r.putTree(ctx, object.NewTree())
		if err != nil {
			return err
		}
		base.Root = addr
	}
	data, err := object.EncodeSnapshot(base)
	if err != nil {
		return err
	}
	addr, err := r.st.Put(ctx, object.KindSnapshot, data)
	if err != nil {
		return err
	}
	return r.st.BranchSet(ctx, r.project, r.stageBranchName(), addr)
}

// StageNote 暂存一条条目写入(不建索引,不进正式分支历史)。
func (r *Repo) StageNote(ctx context.Context, path string, in NoteInput, msg string) (hash.Address, hash.Address, error) {
	if err := r.ensureStageBase(ctx); err != nil {
		return "", "", err
	}
	sr := r.stageView()
	return sr.SetNote(ctx, path, in, "stage: "+msg)
}

// StageRemoveNote 暂存一条条目删除。
func (r *Repo) StageRemoveNote(ctx context.Context, path, msg string) (hash.Address, error) {
	if err := r.ensureStageBase(ctx); err != nil {
		return "", err
	}
	sr := r.stageView()
	return sr.RemoveNote(ctx, path, "stage: "+msg)
}

// StageRemoveDir 暂存一次目录删除(recursive 语义同 RemoveDir)。
// 空目录新增不支持暂存(dir add --stage 在 CLI 层拒绝),删除后空目录自然消失。
func (r *Repo) StageRemoveDir(ctx context.Context, path, msg string, recursive bool) (hash.Address, error) {
	if err := r.ensureStageBase(ctx); err != nil {
		return "", err
	}
	sr := r.stageView()
	return sr.RemoveDir(ctx, path, "stage: "+msg, recursive)
}

// stageSnapshot 返回暂存分支头地址与快照;无暂存分支返回 ErrNoStagedChanges。
func (r *Repo) stageSnapshot(ctx context.Context) (hash.Address, *object.Snapshot, error) {
	head, err := r.st.BranchGet(ctx, r.project, r.stageBranchName())
	if err != nil {
		if errors.Is(err, store.ErrBranchNotFound) {
			return "", nil, ErrNoStagedChanges
		}
		return "", nil, err
	}
	snap, err := r.loadSnapshot(ctx, head)
	if err != nil {
		return "", nil, err
	}
	return head, snap, nil
}

// stageBaseSnapshot 沿 parents 回溯暂存基线快照(Message=stage base)。
func (r *Repo) stageBaseSnapshot(ctx context.Context, head hash.Address) (*object.Snapshot, error) {
	cur := head
	for {
		s, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		if s.Message == stageBaseMessage {
			return s, nil
		}
		if len(s.Parents) == 0 {
			return nil, errors.New("repo: 暂存分支缺少基线快照,请 kb commit --abort 重建暂存")
		}
		cur = s.Parents[0]
	}
}

// StageStatus 返回暂存差异清单(按路径字典序);无暂存时返回空。
func (r *Repo) StageStatus(ctx context.Context) ([]StageChange, error) {
	head, snap, err := r.stageSnapshot(ctx)
	if err != nil {
		if errors.Is(err, ErrNoStagedChanges) {
			return nil, nil
		}
		return nil, err
	}
	baseSnap, err := r.stageBaseSnapshot(ctx, head)
	if err != nil {
		return nil, err
	}
	changes, err := r.stagedChanges(ctx, baseSnap, snap)
	if err != nil {
		return nil, err
	}
	out := make([]StageChange, 0, len(changes))
	for _, ch := range changes {
		out = append(out, StageChange{Op: ch.Op, Path: ch.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// stagedChange 是带目标地址的暂存变更;Removed 为 true 表示暂存删除。
type stagedChange struct {
	Path    string
	Addr    hash.Address
	Removed bool
	Op      string
}

// stagedChanges 对比「基线树 ↔ 暂存树」得到用户暂存的变更集:
// 基线有而暂存无 = 暂存删除;地址不同或基线无 = 暂存写入。结果按路径排序。
func (r *Repo) stagedChanges(ctx context.Context, baseSnap, stageSnap *object.Snapshot) ([]stagedChange, error) {
	baseTree, err := r.loadTree(ctx, baseSnap.Root)
	if err != nil {
		return nil, err
	}
	stageTree, err := r.loadTree(ctx, stageSnap.Root)
	if err != nil {
		return nil, err
	}
	baseLeaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, baseTree, nil, baseLeaves); err != nil {
		return nil, err
	}
	stageLeaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, stageTree, nil, stageLeaves); err != nil {
		return nil, err
	}
	changes := []stagedChange{}
	for path, addr := range stageLeaves {
		if baseLeaves[path] == addr {
			continue
		}
		op := "A"
		if _, ok := baseLeaves[path]; ok {
			op = "M"
		}
		changes = append(changes, stagedChange{Path: path, Addr: addr, Op: op})
	}
	for path := range baseLeaves {
		if _, ok := stageLeaves[path]; !ok {
			changes = append(changes, stagedChange{Path: path, Removed: true, Op: "D"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// CommitStage 把暂存差异合并为当前分支的一个新快照:
// 一次树差异计算 → 一次索引批量增量 → 单快照推进;随后清理暂存分支。
// 语义:以暂存为准覆盖同名路径;main 上其他路径的变更保留(非三方合并)。
func (r *Repo) CommitStage(ctx context.Context, msg string) (hash.Address, int, error) {
	stageHead, stageSnap, err := r.stageSnapshot(ctx)
	if err != nil {
		return "", 0, err
	}
	baseSnap, err := r.stageBaseSnapshot(ctx, stageHead)
	if err != nil {
		return "", 0, err
	}
	changes, err := r.stagedChanges(ctx, baseSnap, stageSnap)
	if err != nil {
		return "", 0, err
	}
	if len(changes) == 0 {
		if err := r.AbortStage(ctx); err != nil {
			return "", 0, err
		}
		return "", 0, ErrNoStagedChanges
	}
	mainTree, _, err := r.currentTree(ctx)
	if err != nil {
		return "", 0, err
	}
	newTree := mainTree.Clone()
	applied := 0
	for _, ch := range changes {
		dirs, slug, err := SplitNotePath(ch.Path)
		if err != nil {
			return "", 0, err
		}
		if ch.Removed {
			if _, werr := r.walkDir(ctx, newTree, dirs); werr != nil {
				applied++ // 目录链在 main 已不存在:删除目标已不在,视为已应用
				continue
			}
			_, err = r.mutateAt(ctx, newTree, dirs, func(dir *object.Tree) error {
				if e, ok := dir.Lookup(slug); ok && e.Type == object.EntryNote {
					dir.Delete(slug)
				}
				return nil
			})
			if err != nil {
				return "", 0, err
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
			return "", 0, err
		}
		applied++
	}
	// 索引:main 旧索引 + 全部暂存差异 = 一次批量增量
	var oldRootAddr hash.Address
	var oldTree *object.Tree
	head, hasHead, err := r.head(ctx)
	if err != nil {
		return "", 0, err
	}
	if hasHead {
		prev, err := r.loadSnapshot(ctx, head)
		if err != nil {
			return "", 0, err
		}
		oldRootAddr = prev.Index
		ot, err := r.treeAtSnapshot(ctx, head)
		if err != nil {
			return "", 0, err
		}
		oldTree = ot
	}
	idxAddr, err := r.updateIndex(ctx, oldRootAddr, oldTree, newTree)
	if err != nil {
		return "", 0, err
	}
	rootAddr, err := r.putTree(ctx, newTree)
	if err != nil {
		return "", 0, err
	}
	snap := &object.Snapshot{Kind: object.KindSnapshot, Root: rootAddr, Time: r.now(), Message: msg, Index: idxAddr}
	if hasHead {
		snap.Parents = []hash.Address{head}
	}
	snapData, err := object.EncodeSnapshot(snap)
	if err != nil {
		return "", 0, err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, snapData)
	if err != nil {
		return "", 0, err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return "", 0, r.translateBranchSetErr(err)
	}
	if err := r.AbortStage(ctx); err != nil {
		return "", 0, err
	}
	return snapAddr, applied, nil
}

// AbortStage 丢弃全部暂存内容(删除暂存分支,暂存快照由 gc 清理)。
func (r *Repo) AbortStage(ctx context.Context) error {
	return r.st.BranchDelete(ctx, r.project, r.stageBranchName())
}
