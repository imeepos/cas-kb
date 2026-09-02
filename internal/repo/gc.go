package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// GCResult 汇报 GC 的标记与清扫计数。
type GCResult struct {
	Marked int
	Swept  int
}

// metaKeyGCKeepLast 是 gc 历史保留水位(meta 表键):值 N>0 表示每个分支
// 仅保留最近 N 个快照的检索索引,更早快照的索引对象可被清扫。
const metaKeyGCKeepLast = "gc.keep_last"

// GC 执行标记-清扫:从各分支头出发做深度感知标记,删除其余未标记对象。
// 存在保留水位(历史索引精简,见 GCWithKeepLast)时,深度 >= 水位的快照
// 只保留本体与数据内容,其检索索引对象被清扫。
// 开启 GCProtect 时,清扫前先把分支表交给 GCExportBranches 备份;
// 备份失败则中止 GC(不清扫任何对象)。
func (r *Repo) GC(ctx context.Context) (GCResult, error) {
	keep, err := r.gcKeepLast(ctx)
	if err != nil {
		return GCResult{}, err
	}
	branches, err := r.st.BranchListAll(ctx)
	if err != nil {
		return GCResult{}, err
	}
	if r.gcProtect {
		if err := r.exportBranches(ctx, branches); err != nil {
			return GCResult{}, err
		}
	}
	depth, err := r.snapshotDepths(ctx, branches)
	if err != nil {
		return GCResult{}, err
	}
	marked := map[string]bool{}
	for addr, d := range depth {
		if err := r.markSnapshotContent(ctx, addr, d, keep, marked); err != nil {
			return GCResult{}, err
		}
	}
	var res GCResult
	res.Marked = len(marked)
	err = r.st.List(ctx, func(info store.ObjectInfo) error {
		if marked[string(info.Addr)] {
			return nil
		}
		if err := r.st.Delete(ctx, info.Addr); err != nil {
			return err
		}
		res.Swept++
		return nil
	})
	if err != nil {
		return GCResult{}, err
	}
	return res, nil
}

// exportBranches 在清扫前导出分支表;未配置导出函数时报错拒绝。
func (r *Repo) exportBranches(ctx context.Context, branches []store.BranchRef) error {
	if r.gcExport == nil {
		return errors.New("repo: GC 保护已开启但未配置分支表导出")
	}
	if err := r.gcExport(ctx, branches); err != nil {
		return fmt.Errorf("repo: GC 前备份分支表失败: %w", err)
	}
	return nil
}

// GCWithKeepLast 设置「每个分支保留最近 keep 个快照的检索索引」的水印
// (持久化到 meta)并执行 GC。被精简的仅是历史快照的索引对象:数据本体/
// 树/历史条目全部保留,代价是被精简快照的 --at 检索不再可用
// (fsck 按水印豁免)。keep<=0 清除水位(停止精简;已精简的历史索引不可恢复)。
func (r *Repo) GCWithKeepLast(ctx context.Context, keep int) (GCResult, error) {
	if keep < 0 {
		return GCResult{}, fmt.Errorf("repo: keep-last 不能为负数")
	}
	if err := r.st.MetaSet(ctx, metaKeyGCKeepLast, strconv.Itoa(keep)); err != nil {
		return GCResult{}, err
	}
	return r.GC(ctx)
}

// gcKeepLast 读取保留水位;未设置或非法视为 0(不限制)。
func (r *Repo) gcKeepLast(ctx context.Context) (int, error) {
	v, err := r.st.MetaGet(ctx, metaKeyGCKeepLast)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, nil
	}
	return n, nil
}

// indexPrunedSnapshots 返回「检索索引已被 gc 精简」的快照地址集合
// (存在 gc.keep_last 水印且 >0 时,按各分支深度 ≥ 水印计算);
// fsck 据此豁免这些快照的 Index 引用检查。
func (r *Repo) indexPrunedSnapshots(ctx context.Context) (map[string]bool, error) {
	v, err := r.st.MetaGet(ctx, metaKeyGCKeepLast)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	keep, err := strconv.Atoi(v)
	if err != nil || keep <= 0 {
		return map[string]bool{}, nil
	}
	branches, err := r.st.BranchListAll(ctx)
	if err != nil {
		return nil, err
	}
	depth, err := r.snapshotDepths(ctx, branches)
	if err != nil {
		return nil, err
	}
	exempt := map[string]bool{}
	for addr, d := range depth {
		if d >= keep {
			exempt[string(addr)] = true
		}
	}
	return exempt, nil
}

// snapshotDepths 从各分支头 BFS,返回每个可达快照的最浅深度
// (跨分支共享祖先取最小;BFS 首达即最小,故不做松弛)。
func (r *Repo) snapshotDepths(ctx context.Context, branches []store.BranchRef) (map[hash.Address]int, error) {
	depth := map[hash.Address]int{}
	queue := make([]hash.Address, 0, len(branches))
	for _, b := range branches {
		if _, ok := depth[b.Addr]; ok {
			continue
		}
		depth[b.Addr] = 0
		queue = append(queue, b.Addr)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		for _, p := range snap.Parents {
			if _, ok := depth[p]; ok {
				continue
			}
			depth[p] = depth[cur] + 1
			queue = append(queue, p)
		}
	}
	return depth, nil
}

// markSnapshotContent 标记一个快照的可达内容。深度 >= keep 时跳过其索引
// (索引对象被清扫,历史条目本体=树/笔记/正文全部保留)。
func (r *Repo) markSnapshotContent(ctx context.Context, addr hash.Address, d, keep int, marked map[string]bool) error {
	if marked[string(addr)] {
		return nil
	}
	marked[string(addr)] = true
	snap, err := r.loadSnapshot(ctx, addr)
	if err != nil {
		return err
	}
	if err := r.markReachable(ctx, snap.Root, marked); err != nil {
		return err
	}
	if snap.Index != "" && (keep <= 0 || d < keep) {
		if err := r.markReachable(ctx, snap.Index, marked); err != nil {
			return err
		}
	}
	return nil
}

// markReachable 从 addr 出发沿引用标记全部可达对象。
func (r *Repo) markReachable(ctx context.Context, addr hash.Address, marked map[string]bool) error {
	key := string(addr)
	if marked[key] {
		return nil
	}
	marked[key] = true
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return fmt.Errorf("repo: GC 标记 %s: %w", addr, err)
	}
	var kids []hash.Address
	switch kind {
	case object.KindNote, object.KindTree, object.KindSnapshot,
		object.KindIndexRoot, object.KindIndexShard:
		kids, err = childrenOf(kind, data)
		if err != nil {
			return fmt.Errorf("repo: GC 解析 %s(%s): %w", addr, kind, err)
		}
	}
	for _, a := range kids {
		if err := r.markReachable(ctx, a, marked); err != nil {
			return err
		}
	}
	return nil
}
