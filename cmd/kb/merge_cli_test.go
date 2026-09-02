package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
)

// M5-B 批次 CLI 测试(名字含 MergeCLI,e2e 为准绳):
// pull --merge 零冲突 / 冲突中间态 / --abort / --continue / --force 互斥 /
// log 双亲展示 / 中间态冻结提示。一律临时 SQLite 双库(两机互写),零外部依赖。

// useDSN 切换 KB_DSN 到指定库(测试结束逐层恢复)。
func useDSN(t *testing.T, dsn string) {
	t.Helper()
	old := os.Getenv("KB_DSN")
	os.Setenv("KB_DSN", dsn)
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("KB_DSN")
		} else {
			os.Setenv("KB_DSN", old)
		}
	})
}

// mergePairCLI 建立两个独立库 A(ours)/B(theirs),各 init,DSN 停在 A。
func mergePairCLI(t *testing.T) (dsnA, dsnB string) {
	t.Helper()
	ctx := context.Background()
	dsnA = testdb.NewSQLite(t)
	dsnB = testdb.NewSQLite(t)
	useDSN(t, dsnA)
	if err := cmdInit(ctx, nil); err != nil {
		t.Fatal(err)
	}
	useDSN(t, dsnB)
	if err := cmdInit(ctx, nil); err != nil {
		t.Fatal(err)
	}
	useDSN(t, dsnA)
	return dsnA, dsnB
}

// mustRun 执行命令并返回 stdout;失败即 Fatal。
func mustRun(t *testing.T, fn func() error) string {
	t.Helper()
	out, err := captureStdout(t, fn)
	if err != nil {
		t.Fatalf("期望成功,得到错误: %v", err)
	}
	return out
}

// failRun 执行预期失败的命令;断言 stdout+stderr 含全部子串,返回 stdout。
func failRun(t *testing.T, want []string, fn func() error) string {
	t.Helper()
	out, err := captureStdout(t, fn)
	if err == nil {
		t.Fatalf("期望失败,实际成功: %q", out)
	}
	combined := out + "\n" + err.Error()
	for _, s := range want {
		if !strings.Contains(combined, s) {
			t.Fatalf("输出应含 %q: err=%v out=%q", s, err, out)
		}
	}
	return out
}

// cliNoteSet 以默认工作流写一条条目(每条即提交)。
func cliNoteSet(t *testing.T, slug, body string) {
	t.Helper()
	if err := cmdNote(context.Background(), []string{"set", slug, "--title", slug, "--body", body, "-m", "set " + slug}); err != nil {
		t.Fatal(err)
	}
}

// parentField 取 log 行的 parent= 字段(时间列含空格,不能按固定下标取)。
func parentField(t *testing.T, line string) string {
	t.Helper()
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, "parent=") {
			return f
		}
	}
	t.Fatalf("log 行缺 parent= 字段: %q", line)
	return ""
}

// seedFork 建立共同基点:A 写 task=v1,B 拉平(两机互写的前提)。
func seedFork(t *testing.T, dsnA, dsnB string) {
	t.Helper()
	useDSN(t, dsnA)
	cliNoteSet(t, "task", "v1")
	useDSN(t, dsnB)
	out := mustRun(t, func() error { return cmdPull(context.Background(), []string{dsnA}) })
	if !strings.Contains(out, "已同步") {
		t.Fatalf("B 拉平 A 应 fast-forward: %q", out)
	}
}

// conflictState 走到「同路径双侧异改」的冲突中间态,DSN 停在 A,返回 A 原头短标识。
func conflictState(t *testing.T, dsnA, dsnB string) string {
	t.Helper()
	useDSN(t, dsnA)
	cliNoteSet(t, "task", "va")
	useDSN(t, dsnB)
	cliNoteSet(t, "task", "vb")
	useDSN(t, dsnA)
	aShort := headShort(t)
	failRun(t, []string{"冲突 1 条", "task", "content", "main-merge"}, func() error {
		return cmdPull(context.Background(), []string{dsnB, "--merge"})
	})
	if headShort(t) != aShort {
		t.Fatalf("冲突不得推进原分支指针: %s != %s", headShort(t), aShort)
	}
	return aShort
}

func TestMergeCLIPullNoConflict(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	useDSN(t, dsnA)
	cliNoteSet(t, "x", "ax body")
	useDSN(t, dsnB)
	cliNoteSet(t, "y", "by body")
	useDSN(t, dsnA)
	// 零冲突一步完成:输出合并短标识与冲突数 0
	out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnB, "--merge"}) })
	if !strings.Contains(out, "冲突 0 条") || !strings.Contains(out, "合并快照 sha256:") {
		t.Fatalf("零冲突输出不符: %q", out)
	}
	if out := mustRun(t, func() error { return cmdFSCK(ctx, nil) }); !strings.Contains(out, "完整,无问题") {
		t.Fatalf("合并后 fsck 应通过: %q", out)
	}
	// 双侧变更同树可见、可检索
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "y"}) }); !strings.Contains(out, "by body") {
		t.Fatalf("theirs 侧条目应可见: %q", out)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"ax"}) }); !strings.Contains(out, "x") {
		t.Fatalf("合并提交应建索引(ours 侧命中): %q", out)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"by"}) }); !strings.Contains(out, "y") {
		t.Fatalf("合并提交应建索引(theirs 侧命中): %q", out)
	}
}

func TestMergeCLIPullConflictState(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	conflictState(t, dsnA, dsnB)
	// 中间态分支存在;原分支内容保持 ours
	if out := mustRun(t, func() error { return cmdBranch(ctx, []string{"ls"}) }); !strings.Contains(out, "main-merge") {
		t.Fatalf("branch ls 应含中间态分支: %q", out)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "task"}) }); !strings.Contains(out, "va") {
		t.Fatalf("冲突未落库,ours 内容应保持: %q", out)
	}
}

func TestMergeCLIMergeAbort(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	aShort := conflictState(t, dsnA, dsnB)
	out := mustRun(t, func() error { return cmdMerge(ctx, []string{"--abort"}) })
	if !strings.Contains(out, "已放弃合并") {
		t.Fatalf("abort 输出不符: %q", out)
	}
	if out := mustRun(t, func() error { return cmdBranch(ctx, []string{"ls"}) }); strings.Contains(out, "main-merge") {
		t.Fatalf("abort 后中间态分支应删除: %q", out)
	}
	if headShort(t) != aShort {
		t.Fatalf("abort 后应回到合并前: %s != %s", headShort(t), aShort)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "task"}) }); !strings.Contains(out, "va") {
		t.Fatalf("abort 后内容应保持 ours: %q", out)
	}
	// 无中间态时收束/放弃均响亮失败并给出指引
	failRun(t, []string{"没有进行中的合并", "pull --merge"}, func() error { return cmdMerge(ctx, nil) })
	failRun(t, []string{"没有进行中的合并"}, func() error { return cmdMerge(ctx, []string{"--continue"}) })
	failRun(t, []string{"没有进行中的合并"}, func() error { return cmdMerge(ctx, []string{"--abort"}) })
}

func TestMergeCLIMergeContinue(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	conflictState(t, dsnA, dsnB)
	// kb stage 在合并中态切换为裁决清单
	out := mustRun(t, func() error { return cmdStage(ctx, nil) })
	if !strings.Contains(out, "存在未完成合并") || !strings.Contains(out, "task") || !strings.Contains(out, "未裁决") {
		t.Fatalf("合并中态 stage 应展示裁决清单: %q", out)
	}
	// 裁决(修正冲突侧)后收束
	mustRun(t, func() error {
		return cmdNote(ctx, []string{"set", "task", "--title", "task", "--body", "resolved draft", "--stage", "-m", "采用合并稿"})
	})
	if out := mustRun(t, func() error { return cmdStage(ctx, nil) }); !strings.Contains(out, "已裁决") {
		t.Fatalf("裁决后 stage 应显示已裁决: %q", out)
	}
	out = mustRun(t, func() error { return cmdMerge(ctx, []string{"--continue", "-m", "merge theirs:task 裁决"}) })
	if !strings.Contains(out, "合并完成") || !strings.Contains(out, "1 条裁决") {
		t.Fatalf("continue 输出不符: %q", out)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "task"}) }); !strings.Contains(out, "resolved draft") {
		t.Fatalf("裁决稿应可见: %q", out)
	}
	// 双亲展示 + 检索 + fsck + 分支清理
	line := strings.SplitN(strings.TrimSpace(mustRun(t, func() error { return cmdLog(ctx, nil) })), "\n", 2)[0]
	if pf := parentField(t, line); !strings.Contains(pf, ",") {
		t.Fatalf("合并快照应显示双亲: %q", line)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"draft"}) }); !strings.Contains(out, "task") {
		t.Fatalf("收束后检索应命中裁决稿: %q", out)
	}
	if out := mustRun(t, func() error { return cmdFSCK(ctx, nil) }); !strings.Contains(out, "完整,无问题") {
		t.Fatalf("收束后 fsck 应通过: %q", out)
	}
	if out := mustRun(t, func() error { return cmdBranch(ctx, []string{"ls"}) }); strings.Contains(out, "main-merge") {
		t.Fatalf("收束后中间态分支应删除: %q", out)
	}
	failRun(t, []string{"没有进行中的合并"}, func() error { return cmdMerge(ctx, nil) })
}

func TestMergeCLIPullForceMutex(t *testing.T) {
	ctx := context.Background()
	_, dsnB := mergePairCLI(t)
	failRun(t, []string{"互斥"}, func() error { return cmdPull(ctx, []string{dsnB, "--force", "--merge"}) })
	failRun(t, []string{"互斥"}, func() error { return cmdPull(ctx, []string{dsnB, "--merge", "--force"}) })
}

func TestMergeCLILogTwoParents(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	useDSN(t, dsnA)
	cliNoteSet(t, "x", "ax body")
	aShort := headShort(t)
	useDSN(t, dsnB)
	cliNoteSet(t, "y", "by body")
	bShort := headShort(t)
	useDSN(t, dsnA)
	mustRun(t, func() error { return cmdPull(ctx, []string{dsnB, "--merge"}) })
	line := strings.SplitN(strings.TrimSpace(mustRun(t, func() error { return cmdLog(ctx, nil) })), "\n", 2)[0]
	parentField(t, line)
	parts := strings.Split(strings.TrimPrefix(parentField(t, line), "parent="), ",")
	if len(parts) != 2 {
		t.Fatalf("合并快照应追加第二亲短标识: %q", line)
	}
	got := map[string]bool{parts[0]: true, parts[1]: true}
	if !got[aShort] || !got[bShort] {
		t.Fatalf("双亲短标识应为两库头: %q (a=%s b=%s)", line, aShort, bShort)
	}
}

func TestMergeCLIFreezeDuringMerge(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB)
	conflictState(t, dsnA, dsnB)
	// 直接写路径一律拒绝且文案可行动
	failRun(t, []string{"未完成合并", "--continue", "--abort"}, func() error {
		return cmdNote(ctx, []string{"set", "task", "--title", "task", "--body", "zz", "-m", "direct"})
	})
	failRun(t, []string{"未完成合并"}, func() error { return cmdPull(ctx, []string{dsnB}) })
	failRun(t, []string{"未完成合并"}, func() error { return cmdPull(ctx, []string{dsnB, "--force"}) })
	failRun(t, []string{"未完成合并"}, func() error { return cmdPull(ctx, []string{dsnB, "--merge"}) })
	failRun(t, []string{"未完成合并"}, func() error { return cmdCommit(ctx, []string{"-m", "x"}) })
	// 读操作不受限
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "task"}) }); !strings.Contains(out, "va") {
		t.Fatalf("冻结只限写,读不应受限: %q", out)
	}
	mustRun(t, func() error { return cmdLog(ctx, nil) })
}
