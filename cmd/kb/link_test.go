package main

import (
	"context"
	"strings"
	"testing"
)

func TestLinkResolveCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cmdNote(ctx, []string{"set", "go/channel", "--title", "Channel", "--body", "b", "-m", "add channel"}))
	must(cmdNote(ctx, []string{"set", "web/only", "--title", "Only", "--body", "b", "-m", "add only"}))
	must(cmdNote(ctx, []string{"set", "web/dup", "--title", "Dup1", "--body", "b", "-m", "add dup1"}))
	must(cmdNote(ctx, []string{"set", "misc/dup", "--title", "Dup2", "--body", "b", "-m", "add dup2"}))

	// 全路径 + 文本输出
	out, err := captureStdout(t, func() error { return cmdLink(ctx, []string{"resolve", "go/channel"}) })
	must(err)
	if !strings.Contains(out, "path:  go/channel") || !strings.Contains(out, "Channel") {
		t.Fatalf("link resolve 输出不符: %q", out)
	}

	// 叶名唯一回退
	out, err = captureStdout(t, func() error { return cmdLink(ctx, []string{"resolve", "only"}) })
	must(err)
	if !strings.Contains(out, "path:  web/only") {
		t.Fatalf("叶名回退应解析 web/only: %q", out)
	}

	// 歧义报错
	err = cmdLink(ctx, []string{"resolve", "dup"})
	if err == nil || !strings.Contains(err.Error(), "web/dup") || !strings.Contains(err.Error(), "misc/dup") {
		t.Fatalf("歧义应列出候选: %v", err)
	}

	// --json 机器契约
	out, err = captureStdout(t, func() error { return cmdLink(ctx, []string{"resolve", "only", "--json"}) })
	must(err)
	if !strings.Contains(out, "\"path\": \"web/only\"") {
		t.Fatalf("link resolve --json 契约不符: %q", out)
	}
}
