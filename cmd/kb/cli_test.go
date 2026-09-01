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

func TestProjectScopeCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Setenv("KB_PROJECT", "alpha")
	if err := cmdProject(ctx, []string{"create", "alpha"}); err != nil {
		t.Fatal(err)
	}
	setNote(t, "a", "A")
	out, err := captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a") {
		t.Fatalf("alpha 的 ls 应含条目 a,got %q", out)
	}
	t.Setenv("KB_PROJECT", "beta")
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(no notes)") {
		t.Fatalf("beta(未创建)的 ls 应提示无条目,got %q", out)
	}
	t.Setenv("KB_PROJECT", "")
	// 尾部 -p:extractProjectArg 应支持任意位置抽取
	rest, proj := extractProjectArg([]string{"note", "ls", "-p", "alpha"})
	if proj != "alpha" || len(rest) != 2 || rest[0] != "note" || rest[1] != "ls" {
		t.Fatalf("尾部 -p 抽取错误: rest=%v proj=%q", rest, proj)
	}
	rest, proj = extractProjectArg([]string{"-p", "beta", "log"})
	if proj != "beta" || len(rest) != 1 || rest[0] != "log" {
		t.Fatalf("前缀 -p 抽取错误: rest=%v proj=%q", rest, proj)
	}
	t.Setenv("KB_PROJECT", "")
	projectOverride = "alpha"
	defer func() { projectOverride = "" }()
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a") {
		t.Fatalf("alpha 作用域 ls 应含 a,got %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdProject(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha\t1") || !strings.Contains(out, "default\t0") {
		t.Fatalf("project ls 应含项目与分支数统计,got %q", out)
	}
}

func TestResetCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "a", "A1")
	setNote(t, "b", "B")
	setNote(t, "c", "C")
	out, err := captureStdout(t, func() error { return cmdLog(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	firstShort := strings.Fields(lines[len(lines)-1])[0] // 最早快照
	if err := cmdReset(ctx, []string{firstShort}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdLog(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("回退后 log 应只剩 1 条,got %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "b") || strings.Contains(out, "c") {
		t.Fatalf("被放弃条目不应出现在 ls: %q", out)
	}
}

func TestNoteGetAtSnapshot(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "a", "A1")
	out, err := captureStdout(t, func() error { return cmdLog(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	s1 := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[0])[0]
	setNote(t, "a", "A2")
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"get", "a"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A2") {
		t.Fatalf("当前内容应为 A2: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"get", "a", "--at", s1}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A1") {
		t.Fatalf("--at 应读旧版本 A1: %q", out)
	}
	if err := cmdNote(ctx, []string{"get", "ghost", "--at", s1}); err == nil {
		t.Fatal("--at 下不存在的 slug 应报错")
	}
}
