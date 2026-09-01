package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
)

// captureStdout 捕获命令函数打印到标准输出的内容。
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	outC := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outC <- buf.String()
	}()
	fnErr := fn()
	os.Stdout = orig
	w.Close()
	return <-outC, fnErr
}

// initRepo 准备一个已迁移的全新测试库并设置 KB_DSN。
func initRepo(t *testing.T) {
	t.Helper()
	t.Setenv("KB_DSN", testdb.New(t))
	if err := cmdInit(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

// setNote 调用 note set 写入一条条目。
func setNote(t *testing.T, slug, title string) {
	t.Helper()
	if err := cmdNote(context.Background(), []string{"set", slug, "--title", title, "--body", "body of " + slug, "-m", "add " + slug}); err != nil {
		t.Fatal(err)
	}
}

// headShort 返回当前分支头的短标识(kb log 首列)。
func headShort(t *testing.T) string {
	t.Helper()
	out, err := captureStdout(t, func() error { return cmdLog(context.Background(), nil) })
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if line == "" {
		t.Fatal("log 应至少有一个快照")
	}
	return strings.Fields(line)[0]
}

func TestDiffByBranchNameAndShortID(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "a", "A")
	mainShort := headShort(t)
	t.Setenv("KB_BRANCH", "dev")
	setNote(t, "b", "B")
	devShort := headShort(t)

	out, err := captureStdout(t, func() error { return cmdDiff(ctx, []string{"main", "dev"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A  b") || !strings.Contains(out, "D  a") {
		t.Fatalf("main->dev 应输出 b added 与 a removed,got %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdDiff(ctx, []string{mainShort, devShort}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A  b") || !strings.Contains(out, "D  a") {
		t.Fatalf("短标识 diff 应输出同样差异,got %q", out)
	}
}

func TestGCBackupCreatedByDefaultAndSkippedWhenOff(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Chdir(t.TempDir())
	setNote(t, "a", "A")

	out, err := captureStdout(t, func() error { return cmdGC(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已备份到") {
		t.Fatalf("保护默认开启应打印备份路径,got %q", out)
	}
	files, _ := filepath.Glob("branches-backup-*.json")
	if len(files) != 1 {
		t.Fatalf("应生成一个备份文件,got %v", files)
	}

	t.Setenv("KB_GC_PROTECT", "off")
	out, err = captureStdout(t, func() error { return cmdGC(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "已备份到") {
		t.Fatalf("保护关闭不应再生成备份,got %q", out)
	}
	files, _ = filepath.Glob("branches-backup-*.json")
	if len(files) != 1 {
		t.Fatalf("关闭后不应新增备份文件,got %v", files)
	}
}
