package repo

// T44 冷启动测试(docs/review/drill-multi-cli.md 缺陷 D1/D2,名字含 MergeCold):
// D1 远端项目存在但分支不存在(零提交)→ pull 已是最新空操作(本地有/双空两形态);
// D2 无共同历史:缺旗标拒绝且新文案、--merge --allow-unrelated 空基线三方合并
// (零冲突自动合 / 同路径冲突进中间态)。一律临时 SQLite 双库,零外部依赖。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/store"
)

// coldPair 开一对独立冷启动库:a=ours(本地),b=theirs(独立远端),
// 两侧均仅 init + project(零提交,分支不存在)。
func coldPair(t *testing.T) (a, b *Repo, aSt, bSt store.Store) {
	t.Helper()
	a, aSt, _ = newRepo(t, "main")
	bSt = openRemote(t)
	ctx := context.Background()
	if err := bSt.ProjectCreate(ctx, "default", "cold remote"); err != nil {
		t.Fatal(err)
	}
	b = Open(bSt, Config{Branch: "main", Now: func() int64 { return fixedTime }})
	return a, b, aSt, bSt
}

// seedCold 各自独立提交(无共同历史,镜像演练剧本:A/B 各自 init 各自写)。
func seedCold(t *testing.T, a, b *Repo, aSt, bSt store.Store) (aHead, bHead hash.Address) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := a.SetNote(ctx, "go/channel", NoteInput{Title: "CA", Body: "a 侧冷启动", Time: fixedTime}, "a cold"); err != nil {
		t.Fatal(err)
	}
	aHead, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "py/decorator", NoteInput{Title: "CB", Body: "b 侧冷启动", Time: fixedTime}, "b cold"); err != nil {
		t.Fatal(err)
	}
	bHead, err = bSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	return aHead, bHead
}

// TestMergeColdPullRemoteEmptyNoop:D1 两形态——双空(本地分支也不存在)与
// 本地有/远端空,均为「已是最新」空操作(UpToDate 且零传输、指针不动);
// 远端项目不存在仍响亮报错(防误配静默)。
func TestMergeColdPullRemoteEmptyNoop(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := coldPair(t)
	// 形态一:双空(两侧均零提交)
	res, err := a.Pull(ctx, bSt, "default", "main", false)
	if err != nil {
		t.Fatalf("双空 pull 应空操作: %v", err)
	}
	if !res.UpToDate || res.Transferred != 0 {
		t.Fatalf("双空 pull 应 UpToDate 零传输: %+v", res)
	}
	if _, has, err := a.head(ctx); err != nil || has {
		t.Fatalf("空操作不得建分支: has=%v err=%v", has, err)
	}
	// 形态二:本地有、远端空
	if _, _, err := a.SetNote(ctx, "local", NoteInput{Title: "L", Body: "local", Time: fixedTime}, "local"); err != nil {
		t.Fatal(err)
	}
	before, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	res, err = a.Pull(ctx, bSt, "default", "main", false)
	if err != nil {
		t.Fatalf("远端空 pull 应空操作: %v", err)
	}
	if !res.UpToDate || res.Transferred != 0 {
		t.Fatalf("远端空 pull 应 UpToDate 零传输: %+v", res)
	}
	after, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil || after != before {
		t.Fatalf("空操作不得动指针: %s != %s err=%v", after, before, err)
	}
	// --force 同样空操作(远端无头无处可指)
	res, err = a.Pull(ctx, bSt, "default", "main", true)
	if err != nil || !res.UpToDate {
		t.Fatalf("--force 对远端空也应空操作: %+v %v", res, err)
	}
	// --merge 同为空操作(与 pull 对称,D1 不留新 asymmetry)
	if _, err := a.MergeStart(ctx, bSt, "default", "main", MergeOptions{}); err != nil {
		t.Fatalf("--merge 对远端空也应空操作: %v", err)
	}
	// 反例:远端项目不存在仍响亮报错(default 项目由库初始化自动播种,
	// 故以未创建的项目名验证误配不被静默吞掉)
	ghost := openRemote(t)
	if _, err := a.Pull(ctx, ghost, "ghost", "main", false); err == nil || !strings.Contains(err.Error(), "分支不存在") {
		t.Fatalf("远端项目不存在应报分支不存在,got %v", err)
	}
	_ = b
}

// TestMergeColdUnrelatedAutoMerge:D2 零冲突自动合——两库各自 init 各自写,
// --allow-unrelated 空基线合并落双亲快照,两侧条目均视为新增全部取入。
func TestMergeColdUnrelatedAutoMerge(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := coldPair(t)
	aHead, bHead := seedCold(t, a, b, aSt, bSt)
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{AllowUnrelated: true})
	if err != nil {
		t.Fatalf("空基线零冲突合并应成功: %v", err)
	}
	if res.Snap == "" || res.Base != "" {
		t.Fatalf("应落合并快照且空基线: snap=%s base=%s", res.Snap, res.Base)
	}
	got := parentsSet(t, a, res.Snap)
	if len(got) != 2 || !got[aHead] || !got[bHead] {
		t.Fatalf("双亲应为两库头: %v (a=%s b=%s)", got, aHead, bHead)
	}
	if res.AutoMerged < 2 {
		t.Fatalf("两侧新增均应自动合: %+v", res)
	}
	// 两侧内容齐备(镜像剧本断言:跨侧条目进入 ours 树)
	if _, err := a.Note(ctx, "go/channel"); err != nil {
		t.Fatalf("ours 侧条目应可见: %v", err)
	}
	ref, err := a.Note(ctx, "py/decorator")
	if err != nil || string(ref.Body) != "b 侧冷启动" {
		t.Fatalf("theirs 侧条目应可见: %v %+v", err, ref)
	}
	// 幂等:再拉已是最新
	res2, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{AllowUnrelated: true})
	if err != nil || !res2.UpToDate {
		t.Fatalf("合并后再拉应已是最新: %+v %v", res2, err)
	}
}

// TestMergeColdUnrelatedConflictState:D2 同路径异地址 → content 冲突进中间态,
// 原分支指针不动、meta 建态,--abort 与既有合并路径同款收场。
func TestMergeColdUnrelatedConflictState(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := coldPair(t)
	_, _ = seedCold(t, a, b, aSt, bSt)
	if _, _, err := a.SetNote(ctx, "shared/x", NoteInput{Title: "X", Body: "a 版", Time: fixedTime}, "a shared"); err != nil {
		t.Fatal(err)
	}
	aHead, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "shared/x", NoteInput{Title: "X", Body: "b 版", Time: fixedTime}, "b shared"); err != nil {
		t.Fatal(err)
	}
	res, err := a.MergeStart(ctx, bSt, "default", "main", MergeOptions{AllowUnrelated: true})
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("同路径异地址应 content 冲突: %v", err)
	}
	if len(mc.Conflicts) != 1 || mc.Conflicts[0].Path != "shared/x" || mc.Conflicts[0].Kind != mergeKindContent {
		t.Fatalf("冲突清单不符: %+v", mc.Conflicts)
	}
	if res.Base != "" {
		t.Fatalf("空基线冲突 res.Base 应为空: %s", res.Base)
	}
	if head, err := aSt.BranchGet(ctx, "default", "main"); err != nil || head != aHead {
		t.Fatalf("冲突不得推进原分支: %s != %s", head, aHead)
	}
	st, err := a.MergeState(ctx)
	if err != nil || st == nil {
		t.Fatalf("应建中间态: st=%v err=%v", st, err)
	}
	if st.Base != "" || len(st.Unresolved) != 1 || st.Unresolved[0] != "shared/x" {
		t.Fatalf("中间态不符: %+v", st)
	}
	// --abort 回到合并前(与既有合并路径相同)
	ab, err := a.MergeAbort(ctx)
	if err != nil || ab.Resolved != 0 {
		t.Fatalf("abort 应成功: %+v %v", ab, err)
	}
	if head, err := aSt.BranchGet(ctx, "default", "main"); err != nil || head != aHead {
		t.Fatalf("abort 后应回到合并前: %s != %s", head, aHead)
	}
	if st, err := a.MergeState(ctx); err != nil || st != nil {
		t.Fatalf("abort 后中间态应清理: %v %v", st, err)
	}
}

// TestMergeColdUnrelatedDefaultRejected:D2 缺旗标拒绝且新文案——
// 无旗标 pull 分叉报「两库无共同历史」并指引 --merge --allow-unrelated,
// 不再断裂指路纯 --merge;--merge 缺旗标同文案拒绝;指针均不动。
func TestMergeColdUnrelatedDefaultRejected(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := coldPair(t)
	aHead, bHead := seedCold(t, a, b, aSt, bSt)
	_, err := a.Pull(ctx, bSt, "default", "main", false)
	if !errors.Is(err, ErrDivergeNoCommonHistory) {
		t.Fatalf("无共同历史无旗标应 ErrDivergeNoCommonHistory: %v", err)
	}
	for _, want := range []string{"两库无共同历史", "--force", "--merge --allow-unrelated", "空基线合并"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("文案应含指定指引: %v", err)
		}
	}
	_, err = a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	if !errors.Is(err, ErrMergeNoCommonHistory) {
		t.Fatalf("--merge 缺旗标应 ErrMergeNoCommonHistory: %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-unrelated") {
		t.Fatalf("--merge 缺旗标文案应指引 --allow-unrelated: %v", err)
	}
	if h, _ := aSt.BranchGet(ctx, "default", "main"); h != aHead {
		t.Fatalf("拒绝不得动 ours: %s != %s", h, aHead)
	}
	if h, _ := bSt.BranchGet(ctx, "default", "main"); h != bHead {
		t.Fatalf("拒绝不得动 theirs: %s != %s", h, bHead)
	}
}
