package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdImport 处理 kb import 子命令(目前仅 md)。
// 用法: kb import md <目录> [-m msg]
// 递归扫描目录下全部 .md 并导入:相对路径去 .md 为条目路径;
// 非 .md 文件、非法路径、front-matter 缺 title 等问题响亮列出并整批拒绝;
// 全部解析成功后走 BulkImport 等价路径(一次提交 + 一次索引增量)。
func cmdImport(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "md" {
		return fmt.Errorf("用法: kb import md <目录> [-m msg]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"-m": true})
	if err != nil {
		return err
	}
	if len(f.pos) != 1 {
		return fmt.Errorf("import md: 缺少源目录")
	}
	root := f.pos[0]
	docs, problems, err := scanMarkdownDir(root)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("import md: 发现 %d 个问题文件,整批拒绝(未写入任何条目):\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	if len(docs) == 0 {
		return fmt.Errorf("import md: 目录 %q 下没有找到 .md 文件", root)
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := r.ImportMarkdown(ctx, docs, f.get("-m", "import md"))
	if err != nil {
		return err
	}
	if res.Snapshot == "" {
		fmt.Printf("import md 0 条(全部 %d 条与当前一致,无新快照)\n", res.Unchanged)
		return nil
	}
	fmt.Printf("import md %d 条(另 %d 条与当前一致)\nsnapshot %s\n", res.Imported, res.Unchanged, res.Snapshot)
	return nil
}

// mdEntryPath 校验相对文件路径并返回条目路径(去 .md 后缀)。
// 非 .md 文件与非法路径(空段 a//b、保留段 . 或 .. 等)返回错误。
func mdEntryPath(rel string) (string, error) {
	if !strings.HasSuffix(rel, ".md") {
		return "", fmt.Errorf("非 .md 文件,无法映射为条目")
	}
	entry := strings.TrimSuffix(rel, ".md")
	if entry == "" {
		return "", fmt.Errorf("条目路径为空")
	}
	if _, err := repo.ParsePath(entry); err != nil {
		return "", err
	}
	return entry, nil
}

// scanMarkdownDir 递归扫描 root 下全部文件并解析为 MdNote。
// 返回(笔记, 问题清单, 错误):问题清单逐文件列出原因(相对路径 + 说明),
// 供整批响亮拒绝;读目录/文件本身的失败直接返回错误。
// 中间段是条目(a.md 与 a/b.md 并存)也计入问题清单。
func scanMarkdownDir(root string) ([]repo.MdNote, []string, error) {
	var docs []repo.MdNote
	var problems []string
	byPath := map[string]string{} // 条目路径 → 文件相对路径(前缀冲突检测)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		entry, perr := mdEntryPath(rel)
		if perr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, perr))
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		doc, derr := repo.DecodeMdNote(entry, data)
		if derr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, derr))
			return nil
		}
		docs = append(docs, doc)
		byPath[entry] = rel
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// 中间段是条目:a.md 与 a/b.md 并存 → a 不能既是条目又是目录
	for entry, rel := range byPath {
		segs := strings.Split(entry, "/")
		for i := 1; i < len(segs); i++ {
			prefix := strings.Join(segs[:i], "/")
			if other, ok := byPath[prefix]; ok {
				problems = append(problems, fmt.Sprintf("%s: 中间段 %q 是条目(%s),不能同时作为目录", rel, prefix, other))
				break
			}
		}
	}
	return docs, problems, nil
}
