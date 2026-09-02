package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/imeepos/cas-kb/internal/view"
)

// T48 契约同构测试:kb stage --json 与 GET /api/v1/merge-state 在同一临时库上
// 逐字段相等(idle 轮询稳态与 merging 冲突中间态两种状态都钉死)。两出口共用
// internal/view.MergeStateRow 一份实现(字段名/字段序/冲突清单契约)。

// assertMergeStateParity 逐字段比较 API 与 CLI 的合并状态行。
func assertMergeStateParity(t *testing.T, name string, api, cli view.MergeStateRow) {
	t.Helper()
	if api.Project != cli.Project {
		t.Errorf("%s: project 不等: API %q vs CLI %q", name, api.Project, cli.Project)
	}
	if api.Branch != cli.Branch {
		t.Errorf("%s: branch 不等: API %q vs CLI %q", name, api.Branch, cli.Branch)
	}
	if api.State != cli.State {
		t.Errorf("%s: state 不等: API %q vs CLI %q", name, api.State, cli.State)
	}
	if api.CanContinue != cli.CanContinue {
		t.Errorf("%s: can_continue 不等: API %v vs CLI %v", name, api.CanContinue, cli.CanContinue)
	}
	if api.CanAbort != cli.CanAbort {
		t.Errorf("%s: can_abort 不等: API %v vs CLI %v", name, api.CanAbort, cli.CanAbort)
	}
	if !reflect.DeepEqual(api.Base, cli.Base) {
		t.Errorf("%s: base 不等: API %v vs CLI %v", name, api.Base, cli.Base)
	}
	if !reflect.DeepEqual(api.Theirs, cli.Theirs) {
		t.Errorf("%s: theirs 不等: API %v vs CLI %v", name, api.Theirs, cli.Theirs)
	}
	if !reflect.DeepEqual(api.Ours, cli.Ours) {
		t.Errorf("%s: ours 不等: API %v vs CLI %v", name, api.Ours, cli.Ours)
	}
	if !reflect.DeepEqual(api.Conflicts, cli.Conflicts) {
		t.Errorf("%s: conflicts 不等:\nAPI  %+v\nCLI  %+v", name, api.Conflicts, cli.Conflicts)
	}
	if api.ConflictCount != cli.ConflictCount {
		t.Errorf("%s: conflict_count 不等: API %d vs CLI %d", name, api.ConflictCount, cli.ConflictCount)
	}
	if !reflect.DeepEqual(api.MergedBranch, cli.MergedBranch) {
		t.Errorf("%s: merged_branch 不等: API %v vs CLI %v", name, api.MergedBranch, cli.MergedBranch)
	}
}

func TestServeMergeStateParity(t *testing.T) {
	ctx := context.Background()
	dsnA, dsnB := mergePairCLI(t)
	seedFork(t, dsnA, dsnB) // 注意:seedFork 结束时 KB_DSN 停在 B
	useDSN(t, dsnA)         // serve 盯住 A:merge state 将建在 A
	base, stop := startAPIServe(t, "")
	defer stop()

	// idle:无合并中态的轮询稳态,两出口同构
	var apiIdle, cliIdle view.MergeStateRow
	apiGetJSON(t, base+"/api/v1/merge-state", &apiIdle)
	cliJSON(t, func() error { return cmdStage(ctx, []string{"--json"}) }, &cliIdle)
	assertMergeStateParity(t, "idle", apiIdle, cliIdle)
	if apiIdle.State != "idle" {
		t.Fatalf("idle 稳态应 state=idle: %+v", apiIdle)
	}

	// merging:冲突中间态(A 侧)下两出口逐字段相等
	conflictState(t, dsnA, dsnB) // DSN 停在 A
	var apiMerge, cliMerge view.MergeStateRow
	apiGetJSON(t, base+"/api/v1/merge-state", &apiMerge)
	cliJSON(t, func() error { return cmdStage(ctx, []string{"--json"}) }, &cliMerge)
	assertMergeStateParity(t, "merging", apiMerge, cliMerge)
	if apiMerge.State != "merging" || len(apiMerge.Conflicts) != 1 || apiMerge.Conflicts[0].Path != "task" {
		t.Fatalf("冲突后应 merging 且 1 条 task 冲突: %+v", apiMerge)
	}
	if apiMerge.MergedBranch == nil || *apiMerge.MergedBranch != "main-merge" {
		t.Fatalf("merged_branch 应为 main-merge: %+v", apiMerge)
	}
}
