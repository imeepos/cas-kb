package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/view"
)

// cmdCommit 提交暂存:一次树差异 → main 单快照 + 一次索引增量 → stage 归零。
// 用法: kb commit [-m msg] [--abort]
func cmdCommit(ctx context.Context, args []string) error {
	f, err := parseFlags(args, map[string]bool{"-m": true})
	if err != nil {
		return err
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if f.has("--abort") {
		if err := r.AbortStage(ctx); err != nil {
			return err
		}
		fmt.Println("暂存已丢弃")
		return nil
	}
	msg := f.get("-m", "commit staged")
	snapAddr, applied, err := r.CommitStage(ctx, msg)
	if err != nil {
		if err == repo.ErrNoStagedChanges {
			fmt.Println("(no staged changes)")
			return nil
		}
		return err
	}
	fmt.Printf("已提交 %d 处变更\nsnapshot %s\n", applied, snapAddr)
	return nil
}

// cmdStage 查看暂存清单;合并中态切换为展示合并裁决清单(调研 §4-4:
// 防止把合并裁决误当普通暂存)。--json 输出合并状态行契约(view.MergeStateRow,
// 与 GET /api/v1/merge-state 同构,idle 为轮询稳态)——两条出口一份实现,
// 由 cmd/kb TestServeMergeStateParity 钉死逐字段相等(T48)。
// 用法: kb stage [--json]
func cmdStage(ctx context.Context, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	ms, err := r.MergeState(ctx)
	if err != nil {
		return err
	}
	if f.has("--json") {
		return printJSON(view.MergeStateRowOf(r.Project(), r.Branch(), ms))
	}
	if ms != nil {
		return printMergeStatus(ms)
	}
	changes, err := r.StageStatus(ctx)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Println("(no staged changes)")
		return nil
	}
	for _, ch := range changes {
		fmt.Printf("%s  %s\n", ch.Op, ch.Path)
	}
	return nil
}
