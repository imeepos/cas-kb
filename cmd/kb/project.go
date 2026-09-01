package main

import (
	"context"
	"fmt"
)

// cmdProject 分发 project 子命令:管理项目命名空间。
func cmdProject(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("project: 缺少子命令(ls/create)")
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "ls":
		names, err := s.ProjectList(ctx)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("project create: 缺少项目名")
		}
		if err := s.ProjectCreate(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("项目 %s 已就绪\n", args[1])
		return nil
	default:
		return fmt.Errorf("project: 未知子命令 %q", args[0])
	}
}
