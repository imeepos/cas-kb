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

func (r *FSCKResult) add(info store.ObjectInfo, problem string) {
	r.Problems = append(r.Problems, FSCKProblem{Addr: string(info.Addr), Kind: string(info.Kind), Problem: problem})
}
