package main

// T44 冷启动 CLI 测试(名字含 MergeCold;e2e 为准绳):
// D1 空远端 pull 输出「已是最新」退出零;D2 无共同历史——缺旗标拒绝且新文案、
// 旗标纪律(单独给或与 --force 同给响亮拒绝)、空基线零冲突自动合、
// 同路径冲突进中间态并以既有 --stage/--continue 闭环。两库互写零外部依赖。

import (
	"context"
	"strings"
	"testing"
)

// TestMergeColdCLIPullRemoteEmptyNoop:D1 两形态——双空与本地有/远端空,
// 均输出「已是最新」且不报错(退出码零)。
func TestMergeColdCLIPullRemoteEmptyNoop(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	// 形态一:双空(两侧均零提交)
	out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnB}) })
	if !strings.Contains(out, "已是最新") {
		t.Fatalf("双空 pull 应已是最新空操作: %q", out)
	}
	// 形态二:本地有、远端空
	useDSN(t, dsnA)
	cliNoteSet(t, "task", "v1")
	out = mustRun(t, func() error { return cmdPull(ctx, []string{dsnB}) })
	if !strings.Contains(out, "已是最新") {
		t.Fatalf("远端空 pull 应已是最新空操作: %q", out)
	}
}

// TestMergeColdCLIUnrelatedNoConflict:D2 端到端——缺旗标拒绝(新文案)、
// 旗标纪律、--merge --allow-unrelated 零冲突落双亲、双侧检索、对侧 ff、幂等 no-op。
func TestMergeColdCLIUnrelatedNoConflict(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	useDSN(t, dsnA)
	cliNoteSet(t, "go/channel", "a 侧内容")
	useDSN(t, dsnB)
	cliNoteSet(t, "py/decorator", "b 侧内容")
	useDSN(t, dsnA)
	// 缺旗标拒绝且新文案(无旗标与仅 --merge 同文案)
	failRun(t, []string{"无共同历史", "--allow-unrelated"}, func() error {
		return cmdPull(ctx, []string{dsnB})
	})
	failRun(t, []string{"无共同历史", "--allow-unrelated"}, func() error {
		return cmdPull(ctx, []string{dsnB, "--merge"})
	})
	// 旗标纪律:与 --force 同给(经互斥)与单独给均响亮拒绝
	failRun(t, []string{"互斥"}, func() error {
		return cmdPull(ctx, []string{dsnB, "--force", "--merge", "--allow-unrelated"})
	})
	failRun(t, []string{"仅与 --merge 连用"}, func() error {
		return cmdPull(ctx, []string{dsnB, "--allow-unrelated"})
	})
	failRun(t, []string{"仅与 --merge 连用"}, func() error {
		return cmdPull(ctx, []string{dsnB, "--force", "--allow-unrelated"})
	})
	// 零冲突空基线合并:双亲快照一步落库
	out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnB, "--merge", "--allow-unrelated"}) })
	if !strings.Contains(out, "冲突 0 条") || !strings.Contains(out, "合并快照 sha256:") {
		t.Fatalf("零冲突输出不符: %q", out)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "py/decorator"}) }); !strings.Contains(out, "b 侧内容") {
		t.Fatalf("theirs 侧新增应可见: %q", out)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"decorator"}) }); !strings.Contains(out, "py/decorator") {
		t.Fatalf("合并应建索引(theirs 侧命中): %q", out)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"channel"}) }); !strings.Contains(out, "go/channel") {
		t.Fatalf("合并应建索引(ours 侧命中): %q", out)
	}
	if out := mustRun(t, func() error { return cmdFSCK(ctx, nil) }); !strings.Contains(out, "完整,无问题") {
		t.Fatalf("合并后 fsck 应通过: %q", out)
	}
	line := strings.SplitN(strings.TrimSpace(mustRun(t, func() error { return cmdLog(ctx, nil) })), "\n", 2)[0]
	if pf := parentField(t, line); !strings.Contains(pf, ",") {
		t.Fatalf("空基线合并快照应双亲: %q", line)
	}
	// 对侧 ff 至合并快照;再拉幂等 no-op(镜像演练剧本收尾)
	useDSN(t, dsnB)
	if out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnA}) }); !strings.Contains(out, "fast-forward") {
		t.Fatalf("对侧应 ff 至合并快照: %q", out)
	}
	if out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnA}) }); !strings.Contains(out, "已是最新") {
		t.Fatalf("对侧再拉应已是最新: %q", out)
	}
	useDSN(t, dsnA)
	if out := mustRun(t, func() error { return cmdPull(ctx, []string{dsnB}) }); !strings.Contains(out, "已是最新") {
		t.Fatalf("合并后再拉应已是最新: %q", out)
	}
}

// TestMergeColdCLIUnrelatedConflictState:D2 同路径异地址 → content 冲突进
// 中间态(空基线标注),theirs 侧非冲突新增照常自动合,--stage/--continue
// 与既有合并路径完全相同。
func TestMergeColdCLIUnrelatedConflictState(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	useDSN(t, dsnA)
	cliNoteSet(t, "shared/x", "a 版内容")
	useDSN(t, dsnB)
	cliNoteSet(t, "shared/x", "b 版内容")
	cliNoteSet(t, "onlyb", "b 独有内容")
	useDSN(t, dsnA)
	aShort := headShort(t)
	failRun(t, []string{"冲突 1 条", "shared/x", "content", "main-merge", "空基线"}, func() error {
		return cmdPull(ctx, []string{dsnB, "--merge", "--allow-unrelated"})
	})
	if headShort(t) != aShort {
		t.Fatalf("冲突不得推进原分支指针: %s != %s", headShort(t), aShort)
	}
	// 裁决清单与既有合并路径同款
	out := mustRun(t, func() error { return cmdStage(ctx, nil) })
	if !strings.Contains(out, "存在未完成合并") || !strings.Contains(out, "shared/x") || !strings.Contains(out, "未裁决") {
		t.Fatalf("合并中态 stage 应展示裁决清单: %q", out)
	}
	mustRun(t, func() error {
		return cmdNote(ctx, []string{"set", "shared/x", "--title", "x", "--body", "裁决稿内容", "--stage", "-m", "采用合并稿"})
	})
	out = mustRun(t, func() error { return cmdMerge(ctx, []string{"--continue", "-m", "merge theirs:cold 裁决"}) })
	if !strings.Contains(out, "合并完成") || !strings.Contains(out, "1 条裁决") {
		t.Fatalf("continue 输出不符: %q", out)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "shared/x"}) }); !strings.Contains(out, "裁决稿内容") {
		t.Fatalf("裁决稿应可见: %q", out)
	}
	if out := mustRun(t, func() error { return cmdNote(ctx, []string{"get", "onlyb"}) }); !strings.Contains(out, "b 独有内容") {
		t.Fatalf("theirs 侧非冲突新增应随空基线自动合进入: %q", out)
	}
	line := strings.SplitN(strings.TrimSpace(mustRun(t, func() error { return cmdLog(ctx, nil) })), "\n", 2)[0]
	if pf := parentField(t, line); !strings.Contains(pf, ",") {
		t.Fatalf("收束快照应双亲: %q", line)
	}
	if out := mustRun(t, func() error { return cmdSearch(ctx, []string{"裁决稿"}) }); !strings.Contains(out, "shared/x") {
		t.Fatalf("收束后检索应命中裁决稿: %q", out)
	}
	if out := mustRun(t, func() error { return cmdFSCK(ctx, nil) }); !strings.Contains(out, "完整,无问题") {
		t.Fatalf("收束后 fsck 应通过: %q", out)
	}
	if out := mustRun(t, func() error { return cmdBranch(ctx, []string{"ls"}) }); strings.Contains(out, "main-merge") {
		t.Fatalf("收束后中间态分支应删除: %q", out)
	}
}
