package repo

import (
	"context"
	"fmt"
	"sort"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// FSCKProblem 记录一次校验发现的问题。
type FSCKProblem struct {
	Addr    string
	Kind    string
	Problem string
}

// FSCKResult 汇报全量校验结果。
type FSCKResult struct {
	Checked  int
	Problems []FSCKProblem
}

// FSCK 全表逐对象重算哈希并校验内部引用完整性。
// 存在 gc 保留水位(gc.keep_last)时,深度 >= 水位的快照允许缺失其检索
// 索引引用(--keep-last 精简语义),其余校验不放宽。
func (r *Repo) FSCK(ctx context.Context) (FSCKResult, error) {
	var res FSCKResult
	exempt, err := r.indexPrunedSnapshots(ctx)
	if err != nil {
		return FSCKResult{}, err
	}
	err = r.st.List(ctx, func(info store.ObjectInfo) error {
		res.Checked++
		if err := r.checkOne(ctx, info, &res, exempt); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return FSCKResult{}, err
	}
	sort.Slice(res.Problems, func(i, j int) bool {
		return res.Problems[i].Addr < res.Problems[j].Addr
	})
	return res, nil
}

func (r *Repo) checkOne(ctx context.Context, info store.ObjectInfo, res *FSCKResult, exempt map[string]bool) error {
	data, kind, err := r.st.Get(ctx, info.Addr)
	if err != nil {
		res.add(info, fmt.Sprint(err))
		return nil
	}
	if hash.Sum(data) != info.Addr {
		res.add(info, "数据与地址不匹配(内容被篡改)")
		return nil
	}
	if kind == object.KindBlob {
		return nil
	}
	if kind == object.KindTree {
		r.checkTreeEntries(ctx, info, data, res)
		return nil
	}
	if kind == object.KindSnapshot {
		return r.checkSnapshot(ctx, info, data, res, exempt)
	}
	kids, err := childrenOf(kind, data)
	if err != nil {
		res.add(info, fmt.Sprintf("解码失败: %v", err))
		return nil
	}
	for _, a := range kids {
		ok, err := r.st.Has(ctx, a)
		if err != nil {
			return err
		}
		if !ok {
			res.add(info, fmt.Sprintf("引用对象缺失: %s", a))
		}
	}
	return nil
}

// checkSnapshot 校验快照引用:Root/Parents 必须存在;Index/Vec 在被保留水位
// 精简的快照上允许缺失(其余情况缺失即问题);Vec 还做向量一致性校验
// (model/dim 与根一致、items 路径存在于本快照;快照无 vec 则跳过不报)。
func (r *Repo) checkSnapshot(ctx context.Context, info store.ObjectInfo, data []byte, res *FSCKResult, exempt map[string]bool) error {
	snap, err := object.DecodeSnapshot(data)
	if err != nil {
		res.add(info, fmt.Sprintf("解码失败: %v", err))
		return nil
	}
	kids := []hash.Address{snap.Root}
	kids = append(kids, snap.Parents...)
	if snap.Index != "" && !exempt[string(info.Addr)] {
		kids = append(kids, snap.Index)
	}
	if snap.Vec != "" && !exempt[string(info.Addr)] {
		kids = append(kids, snap.Vec)
	}
	for _, a := range kids {
		ok, err := r.st.Has(ctx, a)
		if err != nil {
			return err
		}
		if !ok {
			res.add(info, fmt.Sprintf("引用对象缺失: %s", a))
		}
	}
	if snap.Vec != "" && !exempt[string(info.Addr)] {
		r.checkVecIndex(ctx, info, snap, res)
	}
	return nil
}

// checkVecIndex 校验一个快照的语义向量索引(M6-A):
//   - vecroot 可载入且 kind 匹配;
//   - 各 vecshard 的 model/dim 与根一致(向量按 model_id 版本化,混版即问题);
//   - 每个向量项的路径存在于该快照的 root tree(缺失=fail);
//   - 向量项内容可解码且维度与根一致。
//
// 缺失引用已由外层存在性检查报告,此处遇载入失败即返回不重复报。
func (r *Repo) checkVecIndex(ctx context.Context, info store.ObjectInfo, snap *object.Snapshot, res *FSCKResult) {
	root, err := r.LoadVecRoot(ctx, snap.Vec)
	if err != nil {
		res.add(info, fmt.Sprintf("向量索引根损坏: %v", err))
		return
	}
	tree, err := r.loadTree(ctx, snap.Root)
	if err != nil {
		res.add(info, fmt.Sprintf("向量校验需读取 root tree: %v", err))
		return
	}
	leaves := map[string]hash.Address{}
	if err := r.collectLeaves(ctx, tree, nil, leaves); err != nil {
		res.add(info, fmt.Sprintf("向量校验需遍历 root tree: %v", err))
		return
	}
	for bucket, shardAddr := range root.Shards {
		if shardAddr == "" {
			continue
		}
		sh, err := r.LoadVecShard(ctx, shardAddr)
		if err != nil {
			res.add(info, fmt.Sprintf("向量分片损坏(桶 %d): %v", bucket, err))
			continue
		}
		if sh.Model != root.Model || sh.Dim != root.Dim {
			res.add(info, fmt.Sprintf(
				"向量分片(桶 %d)model/dim 为 %q/%d,与 vecroot %q/%d 不一致——疑似跨模型混存,请重跑 kb index rebuild --embed",
				bucket, sh.Model, sh.Dim, root.Model, root.Dim))
			continue
		}
		for _, item := range sh.Items {
			if _, ok := leaves[item.Path]; !ok {
				res.add(info, fmt.Sprintf(
					"向量项路径 %q 不存在于快照(桶 %d)——内容已变更,请重跑 kb index rebuild --embed",
					item.Path, bucket))
				continue
			}
			vec, err := object.DecodeVecBase64(item.Vec)
			if err != nil {
				res.add(info, fmt.Sprintf("向量项 %q 解码失败: %v", item.Path, err))
				continue
			}
			if len(vec) != root.Dim {
				res.add(info, fmt.Sprintf(
					"向量项 %q 维度 %d 与 vecroot %d 不一致", item.Path, len(vec), root.Dim))
			}
		}
	}
}

// checkTreeEntries 校验 tree 条目:目标对象存在,且 kind 与条目类型一致
// (type=note 应指向 note 对象;type=dir 应指向子 tree 对象)。
func (r *Repo) checkTreeEntries(ctx context.Context, info store.ObjectInfo, data []byte, res *FSCKResult) {
	t, err := object.DecodeTree(data)
	if err != nil {
		res.add(info, fmt.Sprintf("解码失败: %v", err))
		return
	}
	for _, e := range t.Entries {
		_, ckind, err := r.st.Get(ctx, e.Addr)
		if err != nil {
			res.add(info, fmt.Sprintf("条目 %q 引用对象缺失: %s", e.Slug, e.Addr))
			continue
		}
		want := object.KindNote
		if e.Type == object.EntryDir {
			want = object.KindTree
		}
		if ckind != want {
			res.add(info, fmt.Sprintf("条目 %q 类型为 %s,但目标对象 %s 是 %s", e.Slug, e.Type, e.Addr, ckind))
		}
	}
}

func (r *FSCKResult) add(info store.ObjectInfo, problem string) {
	r.Problems = append(r.Problems, FSCKProblem{Addr: string(info.Addr), Kind: string(info.Kind), Problem: problem})
}
