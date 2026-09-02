package main

import (
	"context"
	"strings"
	"testing"
)

// TestStageCLIWorkflow 暂存工作流端到端:--stage 累积→status→commit→检索→abort。
func TestStageCLIWorkflow(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cmdNote(ctx, []string{"set", "go/a", "--title", "A", "--body", "alpha", "--stage"}))
	must(cmdNote(ctx, []string{"set", "go/b", "--title", "B", "--body", "beta", "--stage"}))
	// 暂存对 main 不可见
	out, err := captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	must(err)
	if !strings.Contains(out, "(no notes)") {
		t.Fatalf("commit 前 main 应为空: %q", out)
	}
	// 状态清单
	out, err = captureStdout(t, func() error { return cmdStage(ctx, nil) })
	must(err)
	if !strings.Contains(out, "A  go/a") || !strings.Contains(out, "A  go/b") {
		t.Fatalf("stage status 不符: %q", out)
	}
	// 提交
	out, err = captureStdout(t, func() error { return cmdCommit(ctx, []string{"-m", "commit 2"}) })
	must(err)
	if !strings.Contains(out, "已提交 2 处变更") {
		t.Fatalf("commit 输出不符: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	must(err)
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("提交后应 2 条: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdSearch(ctx, []string{"alpha"}) })
	must(err)
	if !strings.Contains(out, "go/a") {
		t.Fatalf("提交后检索应可用: %q", out)
	}
	// 再次 commit:无暂存
	out, err = captureStdout(t, func() error { return cmdCommit(ctx, nil) })
	must(err)
	if !strings.Contains(out, "(no staged changes)") {
		t.Fatalf("应提示无暂存: %q", out)
	}
	// 暂存删除 + abort
	must(cmdNote(ctx, []string{"rm", "go/a", "--stage"}))
	out, err = captureStdout(t, func() error { return cmdStage(ctx, nil) })
	must(err)
	if !strings.Contains(out, "D  go/a") {
		t.Fatalf("stage status 应含 D: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdCommit(ctx, []string{"--abort"}) })
	must(err)
	if !strings.Contains(out, "暂存已丢弃") {
		t.Fatalf("abort 输出不符: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	must(err)
	if !strings.Contains(out, "go/a") {
		t.Fatalf("abort 后 go/a 应保留: %q", out)
	}
}
