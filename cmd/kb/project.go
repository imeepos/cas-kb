package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/store"
)

// cmdProject 分发 project 子命令:管理项目命名空间(ls/create/desc)。
func cmdProject(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("project: 缺少子命令(ls/create/desc)")
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "ls":
		return projectLs(ctx, s, args[1:])
	case "create":
		return projectCreate(ctx, s, args[1:])
	case "desc":
		return projectDesc(ctx, s, args[1:])
	default:
		return fmt.Errorf("project: 未知子命令 %q", args[0])
	}
}

// projectLs 列出项目(名称/描述/分支数);--json 输出机器可读清单(DESIGN §4.6)。
func projectLs(ctx context.Context, s *store.PG, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	stats, err := s.ProjectStats(ctx)
	if err != nil {
		return err
	}
	if f.has("--json") {
		type row struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Branches    int    `json:"branches"`
		}
		rows := make([]row, 0, len(stats))
		for _, st := range stats {
			rows = append(rows, row{Name: st.Project, Description: st.Description, Branches: st.Branches})
		}
		return printJSON(rows)
	}
	for _, st := range stats {
		fmt.Printf("%s\t%d\t%s\n", st.Project, st.Branches, descOrEmpty(st.Description))
	}
	return nil
}

// projectCreate 创建项目,可带 --desc 描述;已存在等价空操作(不覆盖描述)。
func projectCreate(ctx context.Context, s *store.PG, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--desc": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("project create: 缺少项目名")
	}
	desc := f.get("--desc", "")
	if err := checkDesc(desc); err != nil {
		return err
	}
	if err := s.ProjectCreate(ctx, f.pos[0], desc); err != nil {
		return err
	}
	fmt.Printf("项目 %s 已就绪\n", f.pos[0])
	return nil
}

// projectDesc 读取或就地设置项目描述:desc <名称> [描述文本...]。
func projectDesc(ctx context.Context, s *store.PG, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("project desc: 缺少项目名")
	}
	name := args[0]
	if len(args) > 1 {
		desc := strings.TrimSpace(strings.Join(args[1:], " "))
		if err := checkDesc(desc); err != nil {
			return err
		}
		if err := s.ProjectDescribe(ctx, name, desc); err != nil {
			return err
		}
		fmt.Printf("项目 %s 描述已更新\n", name)
		return nil
	}
	stats, err := s.ProjectStats(ctx)
	if err != nil {
		return err
	}
	for _, st := range stats {
		if st.Project == name {
			fmt.Println(descOrEmpty(st.Description))
			return nil
		}
	}
	return store.ErrProjectNotFound
}
