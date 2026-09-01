package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdLink 处理 kb link 子命令(目前仅 resolve)。
func cmdLink(ctx context.Context, args []string) error {
	if len(args) < 1 || args[0] != "resolve" {
		return fmt.Errorf("用法: kb link resolve <slug> [--at 快照] [--json]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"--at": true})
	if err != nil {
		return err
	}
	if len(f.pos) != 1 {
		return fmt.Errorf("link resolve: 缺少 slug")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	var ref *repo.NoteRef
	if at := f.get("--at", ""); at != "" {
		ref, err = r.ResolveLinkAt(ctx, f.pos[0], at)
	} else {
		ref, err = r.ResolveLink(ctx, f.pos[0])
	}
	if err != nil {
		return err
	}
	if f.has("--json") {
		type row struct {
			Path  string `json:"path"`
			Slug  string `json:"slug"`
			Addr  string `json:"addr"`
			Title string `json:"title"`
		}
		return printJSON(row{Path: ref.Path, Slug: ref.Slug, Addr: string(ref.Addr), Title: ref.Note.Meta.Title})
	}
	fmt.Printf("path:  %s\naddr:  %s\ntitle: %s\n", ref.Path, ref.Addr, ref.Note.Meta.Title)
	return nil
}
