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
func (r *Repo) FSCK(ctx context.Context) (FSCKResult, error) {
	var res FSCKResult
	err := r.st.List(ctx, func(info store.ObjectInfo) error {
		res.Checked++
		if err := r.checkOne(ctx, info, &res); err != nil {
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

func (r *Repo) checkOne(ctx context.Context, info store.ObjectInfo, res *FSCKResult) error {
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
