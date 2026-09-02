package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/view"
)

// cmdDiff 处理 kb diff:按全路径输出 A/D/M;--json 输出机器可读清单
// (行契约复用 internal/view,与 /api/v1/diff 同构;差异逻辑只有 repo.Diff 一份)。
func cmdDiff(ctx context.Context, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	if len(f.pos) < 2 {
		return fmt.Errorf("diff: 需要 base 与 tip 两个参数")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	changes, err := r.Diff(ctx, f.pos[0], f.pos[1])
	if err != nil {
		return err
	}
	if f.has("--json") {
		return printJSON(view.DiffRows(changes))
	}
	if len(changes) == 0 {
		fmt.Println("(no changes)")
		return nil
	}
	for _, c := range changes {
		switch c.Type {
		case "added":
			fmt.Printf("A  %s -> %s\n", c.Path, shortAddr(c.To))
		case "removed":
			fmt.Printf("D  %s <- %s\n", c.Path, shortAddr(c.From))
		case "updated":
			fmt.Printf("M  %s %s -> %s\n", c.Path, shortAddr(c.From), shortAddr(c.To))
		}
	}
	return nil
}
