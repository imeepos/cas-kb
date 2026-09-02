package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdBulk 处理 kb bulk 子命令(目前仅 import)。
// 用法: kb bulk import <file.jsonl> [-m msg]
// JSONL 每行: {"path":"go/x","title":"T","tags":["a"],"body":"..."}
func cmdBulk(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "import" {
		return fmt.Errorf("用法: kb bulk import <file.jsonl> [-m msg]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"-m": true})
	if err != nil {
		return err
	}
	if len(f.pos) != 1 {
		return fmt.Errorf("bulk import: 缺少 JSONL 文件")
	}
	file, err := os.Open(f.pos[0])
	if err != nil {
		return fmt.Errorf("bulk import: 打开文件失败: %w", err)
	}
	defer file.Close()
	items := []repo.BulkInput{}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		row := strings.TrimSpace(sc.Text())
		if row == "" {
			continue
		}
		var rec struct {
			Path  string   `json:"path"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
			Body  string   `json:"body"`
		}
		if err := json.Unmarshal([]byte(row), &rec); err != nil {
			return fmt.Errorf("bulk import: 第 %d 行解析失败: %w", line, err)
		}
		items = append(items, repo.BulkInput{Path: rec.Path, In: repo.NoteInput{Title: rec.Title, Body: rec.Body, Tags: rec.Tags}})
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("bulk import: 读取失败: %w", err)
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	snapAddr, n, err := r.BulkImport(ctx, items, f.get("-m", "bulk import"))
	if err != nil {
		return err
	}
	fmt.Printf("bulk import %d 条\nsnapshot %s\n", n, snapAddr)
	return nil
}
