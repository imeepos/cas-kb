package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdMerge 合并收束(调研 §2.5.2):--continue 以暂存/修正后的中间态分支为
// ours 侧重跑落库(成功后清理中间态);--abort 删中间态分支与 meta 键,
// 回到合并前。无旗标时展示合并裁决清单(与 kb stage 在合并中态的展示一致)。
// 用法: kb merge --continue [-m msg] | kb merge --abort | kb merge
func cmdMerge(ctx context.Context, args []string) error {
	f, err := parseFlags(args, map[string]bool{"-m": true, "--msg": true})
	if err != nil {
		return err
	}
	for name := range f.options {
		switch name {
		case "--continue", "--abort", "-m", "--msg":
		default:
			return fmt.Errorf("merge: 不支持的参数 %s(用法: kb merge --continue [-m msg] | --abort)", name)
		}
	}
	if f.has("--continue") && f.has("--abort") {
		return errors.New("merge: --continue 与 --abort 互斥,请二选一")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch {
	case f.has("--continue"):
		cr, err := r.MergeContinue(ctx, f.get("-m", f.get("--msg", "")))
		if err != nil {
			return err
		}
		fmt.Printf("合并完成:%d 条裁决;快照 %s(parents %s %s)\n",
			cr.Resolved, cr.Snap, shortAddr(cr.Ours), shortAddr(cr.Theirs))
	case f.has("--abort"):
		ar, err := r.MergeAbort(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("已放弃合并(丢弃 %d 条裁决),回到合并前\n", ar.Resolved)
	default:
		st, err := r.MergeState(ctx)
		if err != nil {
			return err
		}
		if st == nil {
			return fmt.Errorf("merge: %w", repo.ErrNoMergeState)
		}
		return printMergeStatus(st)
	}
	return nil
}

// printMergeStatus 展示合并中态:冲突清单(路径/类别/三侧短标识)+ 逐条
// 裁决进度 + 收束指引。kb stage 在合并中态切换为本视图(调研 §4-4)。
func printMergeStatus(st *repo.MergeState) error {
	resolved := map[string]bool{}
	for _, p := range st.Resolved {
		resolved[p] = true
	}
	fmt.Printf("存在未完成合并(中间态分支 %s-merge):\n", branchName())
	for _, c := range st.Conflicts {
		mark := "未裁决"
		if resolved[c.Path] {
			mark = "已裁决"
		}
		fmt.Printf("  %s  %s  base %s  ours %s  theirs %s  [%s]\n",
			c.Path, c.Kind, shortAddr(c.Base), shortAddr(c.Ours), shortAddr(c.Theirs), mark)
	}
	fmt.Printf("裁决进度 %d/%d;逐条 kb note set/rm <路径> --stage 裁决后 kb merge --continue,或 kb merge --abort 放弃\n",
		len(st.Resolved), len(st.Conflicts))
	return nil
}
