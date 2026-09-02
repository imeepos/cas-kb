package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/repo"
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

// cmdStage 查看暂存清单。
// 用法: kb stage
func cmdStage(ctx context.Context, args []string) error {
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
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
