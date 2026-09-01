package main

import (
	"context"
	"strings"
	"testing"
)

// TestBackupRestoreWipeCLI 走通 M3.9 运维命令生命周期。
func TestBackupRestoreWipeCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdNote(ctx, []string{"set", "go/conc/channel", "--title", "Channel", "--body", "chan", "-m", "add"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"set", "hello", "--title", "你好", "--body", "first"}); err != nil {
		t.Fatal(err)
	}

	// backup:输出含文件名
	out, err := captureStdout(t, func() error { return cmdBackup(ctx, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "备份完成") || !strings.Contains(out, ".ckb") {
		t.Fatalf("backup 输出不符: %q", out)
	}
	file := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[0])[1]

	// wipe 需 --force
	if err := cmdWipe(ctx, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdWipe(ctx, nil) })
	if err != nil || !strings.Contains(out, "--force") {
		t.Fatalf("无 --force 应只提示不执行: %q %v", out, err)
	}
	// 预览不执行,条目应还在
	out, _ = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if !strings.Contains(out, "hello") {
		t.Fatalf("预览不应清空数据: %q", out)
	}

	// wipe --force
	if err := cmdWipe(ctx, []string{"--force"}); err != nil {
		t.Fatal(err)
	}
	out, _ = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if !strings.Contains(out, "(no notes)") {
		t.Fatalf("清空后应为空: %q", out)
	}

	// restore:非空(空)库直接恢复;读回
	if err := cmdRestore(ctx, []string{file}); err != nil {
		t.Fatal(err)
	}
	out, _ = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if !strings.Contains(out, "go/conc/channel\tChannel") || !strings.Contains(out, "hello\t你好") {
		t.Fatalf("恢复后读回不符: %q", out)
	}

	// 非空库恢复需 --force
	if err := cmdNote(ctx, []string{"set", "extra", "--title", "Extra"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRestore(ctx, []string{file}); err == nil {
		t.Fatal("非空库不加 --force 应拒绝")
	}
	if err := cmdRestore(ctx, []string{file, "--force"}); err != nil {
		t.Fatal(err)
	}
	out, _ = captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if strings.Contains(out, "extra") {
		t.Fatalf("force 恢复应覆盖新增条目: %q", out)
	}
}
