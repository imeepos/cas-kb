package main

import (
	"context"
	"strings"
	"testing"
)

// TestDirLifecycleCLI 走通 M3.8 目录生命周期:建目录 → 嵌套条目 → 视图 → 删除。
func TestDirLifecycleCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)

	// dir add + 嵌套 note set(父目录自动创建)
	if err := cmdDir(ctx, []string{"add", "go"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"set", "go/concurrency/channel", "--title", "Channel", "--body", "chan 语义", "-m", "add channel"}); err != nil {
		t.Fatal(err)
	}

	// note get 按路径;输出应含 path 行
	out, err := captureStdout(t, func() error { return cmdNote(ctx, []string{"get", "go/concurrency/channel"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "path:  go/concurrency/channel") || !strings.Contains(out, "Channel") {
		t.Fatalf("note get 应按路径输出: %q", out)
	}

	// dir ls:目录在前、条目附标题
	if err := cmdDir(ctx, []string{"add", "go/adv"}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"ls", "go"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dir  adv") || !strings.Contains(out, "dir  concurrency") {
		t.Fatalf("dir ls 应列出两个子目录: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"ls", "go/concurrency"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "note channel\tChannel") {
		t.Fatalf("dir ls 应列出条目及标题: %q", out)
	}

	// dir ls --json 机器契约
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"ls", "go", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"name\": \"adv\"") || !strings.Contains(out, "\"name\": \"concurrency\"") || strings.Count(out, "\"type\": \"dir\"") != 2 {
		t.Fatalf("dir ls --json 契约不符: %q", out)
	}

	// dir tree 层级视图(显式项目作用域;全库视图见 dir_tree_global_test.go)
	projectOverride = "default"
	defer func() { projectOverride = "" }()
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"tree"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(root)/") || !strings.Contains(out, "go/") || !strings.Contains(out, "channel  Channel") {
		t.Fatalf("dir tree 输出不符: %q", out)
	}

	// note ls 递归 + --dir 作用域
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go/concurrency/channel\tChannel") {
		t.Fatalf("note ls 应输出全路径: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls", "--dir", "go/concurrency"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "channel") {
		t.Fatalf("note ls --dir 应列出子目录条目: %q", out)
	}

	// dir rm:非空拒绝、--force 递归
	if err := cmdDir(ctx, []string{"rm", "go"}); err == nil {
		t.Fatal("非空目录非递归删除应报错")
	}
	if err := cmdDir(ctx, []string{"rm", "go", "--force"}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(empty)") {
		t.Fatalf("删除后根目录应为空: %q", out)
	}

	// dir add 幂等:重复建目录不报错
	if err := cmdDir(ctx, []string{"add", "x"}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"add", "x"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已存在") {
		t.Fatalf("重复 dir add 应提示已存在: %q", out)
	}
}

// TestNoteSetPathConflictCLI 路径冲突在 CLI 层响亮失败。
func TestNoteSetPathConflictCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdNote(ctx, []string{"set", "a/b", "--title", "AB"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"set", "a/b/c", "--title", "ABC"}); err == nil {
		t.Fatal("中间段是条目时 CLI 应报错")
	}
	if err := cmdNote(ctx, []string{"set", "a//b", "--title", "Bad"}); err == nil {
		t.Fatal("非法路径 CLI 应报错")
	}
}
