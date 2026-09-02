package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdExport 处理 kb export 子命令(目前仅 md)。
// 用法: kb export md <目录> [--at 快照] [--force]
// 把当前分支(或 --at 指定快照)的全部条目导出为镜像 .md 文件树:
// 条目路径 go/concurrency/channel → 文件 <目录>/go/concurrency/channel.md。
// 目标文件已存在时整批拒绝并提示 --force(先预检全部目标,不部分写入)。
func cmdExport(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "md" {
		return fmt.Errorf("用法: kb export md <目录> [--at 快照] [--force]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"--at": true})
	if err != nil {
		return err
	}
	if len(f.pos) != 1 {
		return fmt.Errorf("export md: 缺少目标目录")
	}
	dir := f.pos[0]
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	docs, err := r.ExportMarkdown(ctx, f.get("--at", ""))
	if err != nil {
		return err
	}
	type target struct {
		doc  repo.MdNote
		full string
	}
	targets := make([]target, 0, len(docs))
	var conflicts []string
	for _, d := range docs {
		rel := d.Path + ".md"
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if !f.has("--force") {
			if _, err := os.Stat(full); err == nil {
				conflicts = append(conflicts, rel)
				continue
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("export md: 检查 %s 失败: %w", rel, err)
			}
		}
		targets = append(targets, target{doc: d, full: full})
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("export md: %d 个文件已存在,拒绝覆盖;确认后加 --force 整批覆盖:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "))
	}
	for _, t := range targets {
		if err := os.MkdirAll(filepath.Dir(t.full), 0o755); err != nil {
			return fmt.Errorf("export md: 创建目录失败: %w", err)
		}
		if err := os.WriteFile(t.full, repo.EncodeMdNote(t.doc), 0o644); err != nil {
			return fmt.Errorf("export md: 写入 %s 失败: %w", t.doc.Path, err)
		}
	}
	at := ""
	if v := f.get("--at", ""); v != "" {
		at = fmt.Sprintf(" (--at %s)", v)
	}
	fmt.Printf("export md %d 条 → %s%s\n", len(docs), dir, at)
	return nil
}
