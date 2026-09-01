package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/store"
)

// cmdBranch 分发 branch 子命令:项目作用域内的分支清单与描述。
func cmdBranch(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("branch: 缺少子命令(ls/desc)")
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "ls":
		return branchLs(ctx, s, args[1:])
	case "desc":
		return branchDesc(ctx, s, args[1:])
	default:
		return fmt.Errorf("branch: 未知子命令 %q", args[0])
	}
}

// branchLs 列出当前项目作用域的全部分支;--json 输出机器可读清单(DESIGN §4.6)。
func branchLs(ctx context.Context, s *store.PG, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	refs, err := s.BranchList(ctx, projectName())
	if err != nil {
		return err
	}
	if f.has("--json") {
		type row struct {
			Name        string `json:"name"`
			Addr        string `json:"addr"`
			Description string `json:"description"`
		}
		rows := make([]row, 0, len(refs))
		for _, r := range refs {
			rows = append(rows, row{Name: r.Name, Addr: string(r.Addr), Description: r.Description})
		}
		return printJSON(rows)
	}
	for _, r := range refs {
		fmt.Printf("%s\t%s\t%s\n", r.Name, r.Addr, r.Description)
	}
	return nil
}

// branchDesc 读取或就地设置分支描述:desc <名称> [描述文本...]。
func branchDesc(ctx context.Context, s *store.PG, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("branch desc: 缺少分支名")
	}
	name := args[0]
	if len(args) > 1 {
		desc := strings.TrimSpace(strings.Join(args[1:], " "))
		if err := checkDesc(desc); err != nil {
			return err
		}
		if err := s.BranchDescribe(ctx, projectName(), name, desc); err != nil {
			return err
		}
		fmt.Printf("分支 %s 描述已更新\n", name)
		return nil
	}
	refs, err := s.BranchList(ctx, projectName())
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.Name == name {
			fmt.Println(descOrEmpty(r.Description))
			return nil
		}
	}
	return store.ErrBranchNotFound
}
