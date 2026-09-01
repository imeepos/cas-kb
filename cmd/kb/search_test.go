package main

import (
	"context"
	"strings"
	"testing"
)

// TestSearchCLI 走通 CLI 检索:文本输出、JSON 契约、确定性。
func TestSearchCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cmdNote(ctx, []string{"set", "go/channel", "--title", "Channel 并发", "--body", "chan 语义", "-m", "a"}))
	must(cmdNote(ctx, []string{"set", "misc/other", "--title", "Other", "--body", "别的", "-m", "b"}))

	out, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel"}) })
	must(err)
	if !strings.Contains(out, "go/channel") || !strings.Contains(out, "Channel") {
		t.Fatalf("search 文本输出不符: %q", out)
	}
	out2, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel"}) })
	must(err)
	if out != out2 {
		t.Fatal("同快照同查询输出应完全一致")
	}
	out3, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"channel", "--json"}) })
	must(err)
	if !strings.Contains(out3, "\"path\": \"go/channel\"") {
		t.Fatalf("search --json 契约不符: %q", out3)
	}
	out4, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"查询不到的词"}) })
	must(err)
	if !strings.Contains(out4, "(no results)") {
		t.Fatalf("无命中应提示: %q", out4)
	}
}
