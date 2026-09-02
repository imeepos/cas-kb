package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestGCKeepLastCLI --keep-last 旗标:设置保留策略并执行 gc;数据与检索不受影响。
func TestGCKeepLastCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Setenv("KB_GC_PROTECT", "off")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 12; i++ {
		must(cmdNote(ctx, []string{"set", fmt.Sprintf("n%d", i), "--title", fmt.Sprintf("H%d", i), "--body", fmt.Sprintf("b%d", i), "-m", "h"}))
	}
	out, err := captureStdout(t, func() error { return cmdGC(ctx, []string{"--keep-last", "5"}) })
	must(err)
	if !strings.Contains(out, "保留策略: 最近 5 个快照") || !strings.Contains(out, "GC: 标记") {
		t.Fatalf("gc --keep-last 输出不符: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	must(err)
	if strings.Count(out, "\n") != 12 {
		t.Fatalf("数据本体应全部保留: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdFSCK(ctx, nil) })
	must(err)
	if !strings.Contains(out, "完整,无问题") {
		t.Fatalf("fsck 应干净: %q", out)
	}
	if err = cmdGC(ctx, []string{"--keep-last", "abc"}); err == nil || !strings.Contains(err.Error(), "非负整数") {
		t.Fatalf("非法 keep-last 应报错: %v", err)
	}
}
