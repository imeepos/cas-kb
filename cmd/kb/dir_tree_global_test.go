package main

import (
	"context"
	"strings"
	"testing"
)

// M3.11:dir tree 全库视图 —— 未显式指定项目(-p/KB_PROJECT 均未设置)时,
// (root)/ 下以项目为顶层节点,逐项目挂其默认分支树;显式 -p 保持单项目视图。
func TestDirTreeGlobalView(t *testing.T) {
	initRepo(t)
	ctx := context.Background()
	t.Cleanup(func() { projectOverride = "" })

	// default 项目写一条
	setNote(t, "hello", "你好")

	// 第二个项目写一条
	if err := cmdProject(ctx, []string{"create", "lab"}); err != nil {
		t.Fatal(err)
	}
	projectOverride = "lab"
	setNote(t, "go/chan", "通道")
	projectOverride = ""

	// 空项目(无分支)
	if err := cmdProject(ctx, []string{"create", "void"}); err != nil {
		t.Fatal(err)
	}

	// 不带 -p/KB_PROJECT:全库视图
	out, err := captureStdout(t, func() error { return cmdDir(ctx, []string{"tree"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(root)/", "lab/", "default/", "通道", "你好", "(空)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("全库树缺 %q: %q", want, out)
		}
	}

	// KB_PROJECT 显式设置时:单项目视图
	t.Setenv("KB_PROJECT", "lab")
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"tree"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "通道") || strings.Contains(out, "你好") || strings.Contains(out, "default/") {
		t.Fatalf("KB_PROJECT 显式作用域不应展示全库: %q", out)
	}
	t.Setenv("KB_PROJECT", "")

	// -p 显式指定时:单项目视图
	projectOverride = "lab"
	out, err = captureStdout(t, func() error { return cmdDir(ctx, []string{"tree"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(root)/") || !strings.Contains(out, "通道") {
		t.Fatalf("单项目树输出不符: %q", out)
	}
	if strings.Contains(out, "default/") || strings.Contains(out, "你好") {
		t.Fatalf("显式 -p 不应串项目: %q", out)
	}
}
