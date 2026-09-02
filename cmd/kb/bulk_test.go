package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBulkCLI 批量导入 JSONL → 单快照 → 检索可用。
func TestBulkCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	var rows strings.Builder
	for i := 1; i <= 50; i++ {
		rows.WriteString(fmt.Sprintf(`{"path":"t%d/n%d","title":"B%d channel","tags":["bulk"],"body":"第 %d 条 bulk 内容 channel"}`+"\n", i%4, i, i, i))
	}
	p := filepath.Join(t.TempDir(), "bulk.jsonl")
	if err := os.WriteFile(p, []byte(rows.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdBulk(ctx, []string{"import", p, "-m", "bulk 50"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bulk import 50 条") {
		t.Fatalf("bulk 输出不符: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "-n", "5"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "channel") {
		t.Fatalf("bulk 后检索应可用: %q", out)
	}
}
