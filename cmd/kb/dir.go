package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

// cmdDir 分发 dir 子命令。
func cmdDir(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("dir: 缺少子命令(add/ls/rm/tree)")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "add":
		return dirAdd(ctx, r, args[1:])
	case "ls":
		return dirLs(ctx, r, args[1:])
	case "rm":
		return dirRm(ctx, r, args[1:])
	case "tree":
		return dirTree(ctx, r, s, args[1:])
	default:
		return fmt.Errorf("dir: 未知子命令 %q", args[0])
	}
}

// dirAdd 创建目录(mkdir -p 语义;已存在且非空则幂等)。
func dirAdd(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, map[string]bool{"-m": true, "--msg": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("dir add: 缺少目录路径")
	}
	msg := f.get("-m", f.get("--msg", "dir add"))
	snapAddr, created, err := r.Mkdir(ctx, f.pos[0], msg)
	if err != nil {
		return err
	}
	if !created {
		fmt.Printf("dir %s 已存在\n", f.pos[0])
		return nil
	}
	fmt.Printf("dir %s\nsnapshot %s\n", f.pos[0], snapAddr)
	return nil
}

// dirLs 列出目录的直接子项(目录在前)。
func dirLs(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	path := ""
	if len(f.pos) >= 1 {
		path = f.pos[0]
	}
	entries, err := r.DirLs(ctx, path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	if f.has("--json") {
		type row struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Title string `json:"title,omitempty"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			title := ""
			if e.Type == object.EntryNote {
				ref, err := r.Note(ctx, childPath(path, e.Name))
				if err != nil {
					return err
				}
				title = ref.Note.Meta.Title
			}
			rows = append(rows, row{Name: e.Name, Type: string(e.Type), Title: title})
		}
		return printJSON(rows)
	}
	for _, e := range entries {
		if e.Type == object.EntryDir {
			fmt.Printf("dir  %s\n", e.Name)
			continue
		}
		ref, err := r.Note(ctx, childPath(path, e.Name))
		if err != nil {
			return err
		}
		fmt.Printf("note %s\t%s\n", e.Name, ref.Note.Meta.Title)
	}
	return nil
}

// dirRm 删除目录;非空目录需要 --force 递归删除。
func dirRm(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, map[string]bool{"-m": true, "--msg": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("dir rm: 缺少目录路径")
	}
	msg := f.get("-m", f.get("--msg", "dir rm"))
	snapAddr, err := r.RemoveDir(ctx, f.pos[0], msg, f.has("--force"))
	if err != nil {
		return err
	}
	fmt.Printf("removed dir %s\nsnapshot %s\n", f.pos[0], snapAddr)
	return nil
}

// dirTree 输出目录层级树(note 附标题)。
// 未显式指定项目(-p/KB_PROJECT 均未设置)且未给路径时,渲染全库视图:
// (root)/ 下项目为顶层节点(M3.11,DESIGN §4.6);显式指定项目保持单项目树。
func dirTree(ctx context.Context, r *repo.Repo, s store.Store, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	path := ""
	if len(f.pos) >= 1 {
		path = f.pos[0]
	}
	if path == "" && !projectExplicitlySet() {
		return dirTreeAll(ctx, s)
	}
	node, err := r.DirTree(ctx, path)
	if err != nil {
		return err
	}
	name := node.Name
	if node.Path == "" {
		name = "(root)"
	}
	fmt.Println(name + "/")
	renderTree(node.Children, "")
	return nil
}

// dirTreeAll 全库视图:项目清单作为 (root)/ 的顶层,逐项目渲染其默认分支树。
func dirTreeAll(ctx context.Context, s store.Store) error {
	stats, err := s.ProjectStats(ctx)
	if err != nil {
		return err
	}
	fmt.Println("(root)/")
	for i, st := range stats {
		branch, next := "├── ", "│   "
		if i == len(stats)-1 {
			branch, next = "└── ", "    "
		}
		fmt.Printf("%s%s/\n", branch, st.Project)
		if _, err := s.BranchGet(ctx, st.Project, branchName()); err != nil {
			if !errors.Is(err, store.ErrBranchNotFound) {
				return err
			}
			fmt.Printf("%s└── (空)\n", next)
			continue
		}
		pr := repo.Open(s, repo.Config{Project: st.Project, Branch: branchName()})
		node, err := pr.DirTree(ctx, "")
		if err != nil {
			return err
		}
		renderTree(node.Children, next)
	}
	return nil
}

// renderTree 递归渲染层级树。
func renderTree(nodes []*repo.DirNode, indent string) {
	for i, n := range nodes {
		branch, next := "├── ", "│   "
		if i == len(nodes)-1 {
			branch, next = "└── ", "    "
		}
		if n.Type == object.EntryDir {
			fmt.Printf("%s%s%s/\n", indent, branch, n.Name)
			renderTree(n.Children, indent+next)
			continue
		}
		line := n.Name
		if n.Title != "" {
			line += "  " + n.Title
		}
		fmt.Printf("%s%s%s\n", indent, branch, line)
	}
}

// childPath 拼接目录路径与子名。
func childPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
