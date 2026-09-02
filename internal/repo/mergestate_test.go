package repo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/store"
)

// M5-B 批次测试(docs/research/merge-design.md §2.5.2/§4-4):
// 冲突中间态(<branch>-merge 分支 + meta 键)、kb merge --continue/--abort
// 收束语义、冻结纪律、--stage 裁决路由。与 A 批次内核测试(merge_test.go)
// 互补;一律临时库,零外部依赖。

// divergeSamePath 构造同路径双侧异改的分叉,发起 MergeStart(预期冲突建态)。
// 返回合并结果(含 Base/Ours/Theirs/Conflicts)与冲突错误。
func divergeSamePath(t *testing.T, a, b *Repo, aSt, bSt store.Store) (MergeResult, error) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p0", Time: fixedTime}, "p0"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p1 ours", Time: fixedTime}, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p2 theirs", Time: fixedTime}, "p2"); err != nil {
		t.Fatal(err)
	}
	return a.MergeStart(ctx, bSt, "default", "main", MergeOptions{})
}

func wantMerging(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "未完成合并") {
		t.Fatalf("%s 应被冻结拒绝(未完成合并),got %v", what, err)
	}
}

func TestMergeStateConflictBeginContinue(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	res, err := divergeSamePath(t, a, b, aSt, bSt)
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("双侧异改应报冲突,got %v", err)
	}
	// 中间态:meta 键(base/theirs/ours/冲突清单)+ -merge 分支基线快照
	st, err := a.MergeState(ctx)
	if err != nil || st == nil {
		t.Fatalf("冲突后应存在合并中间态: st=%v err=%v", st, err)
	}
	if st.Base != res.Base || st.Theirs != res.Theirs || st.Ours != res.Ours {
		t.Fatalf("中间态地址不符: %+v vs res %+v", st, res)
	}
	if len(st.Conflicts) != 1 || st.Conflicts[0].Path != "p" || st.Unresolved == nil || len(st.Unresolved) != 1 {
		t.Fatalf("冲突清单/进度不符: %+v", st)
	}
	mergeHead, err := aSt.BranchGet(ctx, "default", "main-merge")
	if err != nil {
		t.Fatalf("main-merge 分支应存在: %v", err)
	}
	baseSnap, err := a.loadSnapshot(ctx, mergeHead)
	if err != nil {
		t.Fatal(err)
	}
	if baseSnap.Message != mergeBaseMessage || len(baseSnap.Parents) != 1 || baseSnap.Parents[0] != res.Ours || baseSnap.Index != "" {
		t.Fatalf("基线快照形态不符(merge base / parents=[ours] / 无索引): %+v", baseSnap)
	}
	// 冻结纪律:一切直接写路径响亮拒绝
	_, _, err = a.SetNote(ctx, "q", NoteInput{Title: "Q", Body: "q", Time: fixedTime}, "x")
	wantMerging(t, err, "note set")
	_, err = a.RemoveNote(ctx, "p", "x")
	wantMerging(t, err, "note rm")
	_, _, err = a.Mkdir(ctx, "d", "x")
	wantMerging(t, err, "dir add")
	_, err = a.RemoveDir(ctx, "workflow", "x", true)
	wantMerging(t, err, "dir rm")
	_, _, err = a.BulkImport(ctx, []BulkInput{{Path: "b", In: NoteInput{Title: "B", Body: "b", Time: fixedTime}}}, "x")
	wantMerging(t, err, "bulk import")
	_, err = a.Reset(ctx, string(res.Base))
	wantMerging(t, err, "reset")
	_, _, err = a.RebuildIndex(ctx, "x")
	wantMerging(t, err, "index rebuild")
	_, _, err = a.CommitStage(ctx, "x")
	wantMerging(t, err, "commit")
	_, err = a.Pull(ctx, bSt, "default", "main", false)
	wantMerging(t, err, "pull")
	_, err = a.Pull(ctx, bSt, "default", "main", true)
	wantMerging(t, err, "pull --force")
	// 读操作不受限
	if _, err := a.Note(ctx, "p"); err != nil {
		t.Fatalf("冻结只限写,读不应受限: %v", err)
	}
	// --stage 升格为裁决动作:写入 -merge 视图(而非 -stage)
	if _, _, err := a.StageNote(ctx, "p", NoteInput{Title: "P", Body: "p resolved", Time: fixedTime}, "采用合并稿"); err != nil {
		t.Fatalf("裁决写入应成功: %v", err)
	}
	if _, err := aSt.BranchGet(ctx, "default", "main-stage"); !errors.Is(err, store.ErrBranchNotFound) {
		t.Fatalf("裁决不得触碰 -stage 暂存分支: %v", err)
	}
	st, err = a.MergeState(ctx)
	if err != nil || st == nil || len(st.Resolved) != 1 || len(st.Unresolved) != 0 {
		t.Fatalf("裁决后进度应为 1/0: %+v err=%v", st, err)
	}
	// --continue 收束:双亲合并快照 + 清理中间态
	cr, err := a.MergeContinue(ctx, "merge theirs:p 裁决")
	if err != nil {
		t.Fatalf("continue 应成功: %v", err)
	}
	if cr.Resolved != 1 {
		t.Fatalf("应应用 1 条裁决,got %d", cr.Resolved)
	}
	head, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil || head != cr.Snap {
		t.Fatalf("分支头应为合并快照: head=%s snap=%s err=%v", head, cr.Snap, err)
	}
	snap, err := a.loadSnapshot(ctx, cr.Snap)
	if err != nil {
		t.Fatal(err)
	}
	// 双亲为无序对(EncodeSnapshot 按地址规范排序保证字节稳定,与内核测试同口径)
	if len(snap.Parents) != 2 {
		t.Fatalf("合并快照应双亲: %+v", snap.Parents)
	}
	parents := map[hash.Address]bool{}
	for _, p := range snap.Parents {
		parents[p] = true
	}
	if !parents[res.Ours] || !parents[res.Theirs] {
		t.Fatalf("双亲应为 ours/theirs 头: %+v res=%+v", snap.Parents, res)
	}
	if snap.Index == "" {
		t.Fatal("正式合并提交必须建索引")
	}
	if _, err := aSt.BranchGet(ctx, "default", "main-merge"); !errors.Is(err, store.ErrBranchNotFound) {
		t.Fatalf("-merge 分支应清理: %v", err)
	}
	if st, err := a.MergeState(ctx); err != nil || st != nil {
		t.Fatalf("meta 键应清理: st=%v err=%v", st, err)
	}
	// 数据与索引
	ref, err := a.Note(ctx, "p")
	if err != nil || string(ref.Body) != "p resolved" {
		t.Fatalf("裁决稿应可见: %v %+v", err, ref)
	}
	hits, err := a.Search(ctx, "resolved", "")
	if err != nil || len(hits) == 0 || hits[0].Path != "p" {
		t.Fatalf("合并提交检索应命中裁决稿: %v %+v", err, hits)
	}
	// 冻结解除
	if _, _, err := a.SetNote(ctx, "q", NoteInput{Title: "Q", Body: "q", Time: fixedTime}, "after"); err != nil {
		t.Fatalf("收束后写路径应恢复: %v", err)
	}
}

func TestMergeStateZeroAdjudicationRefused(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	if _, err := divergeSamePath(t, a, b, aSt, bSt); err == nil {
		t.Fatal("应报冲突")
	}
	// 零裁决 continue 响亮拒绝:冲突条目静默保持 ours 占位等于丢 theirs 变更
	_, err := a.MergeContinue(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "没有任何裁决") {
		t.Fatalf("零裁决 continue 应拒绝,got %v", err)
	}
	if st, err := a.MergeState(ctx); err != nil || st == nil {
		t.Fatalf("拒绝后中间态应保留: st=%v err=%v", st, err)
	}
}

func TestMergeStateAbort(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	res, err := divergeSamePath(t, a, b, aSt, bSt)
	if err == nil {
		t.Fatal("应报冲突")
	}
	if _, _, err := a.StageNote(ctx, "p", NoteInput{Title: "P", Body: "p resolved", Time: fixedTime}, "裁决"); err != nil {
		t.Fatal(err)
	}
	ar, err := a.MergeAbort(ctx)
	if err != nil {
		t.Fatalf("abort 应成功: %v", err)
	}
	if ar.Resolved != 1 {
		t.Fatalf("应报告放弃的裁决条数 1,got %d", ar.Resolved)
	}
	if _, err := aSt.BranchGet(ctx, "default", "main-merge"); !errors.Is(err, store.ErrBranchNotFound) {
		t.Fatalf("-merge 分支应删除: %v", err)
	}
	if st, err := a.MergeState(ctx); err != nil || st != nil {
		t.Fatalf("meta 键应删除: st=%v err=%v", st, err)
	}
	head, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil || head != res.Ours {
		t.Fatalf("原分支指针应不动: head=%s ours=%s err=%v", head, res.Ours, err)
	}
	ref, err := a.Note(ctx, "p")
	if err != nil || string(ref.Body) != "p1 ours" {
		t.Fatalf("abort 后应保持 ours 内容: %v %+v", err, ref)
	}
	// 收束/放弃在无中间态时均响亮失败
	if _, err := a.MergeContinue(ctx, ""); !errors.Is(err, ErrNoMergeState) {
		t.Fatalf("无中间态 continue 应 ErrNoMergeState,got %v", err)
	}
	if _, err := a.MergeAbort(ctx); !errors.Is(err, ErrNoMergeState) {
		t.Fatalf("无中间态 abort 应 ErrNoMergeState,got %v", err)
	}
	// 冻结解除
	if _, _, err := a.SetNote(ctx, "q", NoteInput{Title: "Q", Body: "q", Time: fixedTime}, "after"); err != nil {
		t.Fatalf("abort 后写路径应恢复: %v", err)
	}
}

func TestMergeStateNoCommonHistoryLeavesNoState(t *testing.T) {
	ctx := context.Background()
	a, b, _, bSt := mergePair(t, fixedTime)
	// 两库各自独立提交,无共同祖先
	if _, _, err := a.SetNote(ctx, "x", NoteInput{Title: "X", Body: "x", Time: fixedTime}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "y", NoteInput{Title: "Y", Body: "y", Time: fixedTime}, "y"); err != nil {
		t.Fatal(err)
	}
	_, err := a.MergeStart(ctx, bSt, "default", "main", MergeOptions{})
	if !errors.Is(err, ErrMergeNoCommonHistory) {
		t.Fatalf("无共同历史应响亮拒绝,got %v", err)
	}
	if st, err := a.MergeState(ctx); err != nil || st != nil {
		t.Fatalf("拒绝时不得建中间态: st=%v err=%v", st, err)
	}
}

func TestMergeStateStartRefusesWhenAlreadyMerging(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	if _, err := divergeSamePath(t, a, b, aSt, bSt); err == nil {
		t.Fatal("应报冲突")
	}
	_, err := a.MergeStart(ctx, bSt, "default", "main", MergeOptions{})
	wantMerging(t, err, "已有中间态的 pull --merge")
	// 内核 Merge 同受冻结守卫保护(防御性,防绕过 MergeStart 的调用方)
	_, err = a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	wantMerging(t, err, "内核 Merge")
}

func TestMergeStateModifyDeleteAdjudication(t *testing.T) {
	// 删改对撞的裁决面:note rm --stage 接受删除(与 note set 重写对称),
	// 收束后条目消失、fsck 零问题
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	if _, _, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p0", Time: fixedTime}, "p0"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p1 ours", Time: fixedTime}, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RemoveNote(ctx, "p", "theirs rm p"); err != nil {
		t.Fatal(err)
	}
	res, err := a.MergeStart(ctx, bSt, "default", "main", MergeOptions{})
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) || len(res.Conflicts) != 1 || res.Conflicts[0].Kind != mergeKindModifyDelete {
		t.Fatalf("应报 modify-delete 冲突: %v %v", res.Conflicts, err)
	}
	if _, err := a.StageRemoveNote(ctx, "p", "接受 theirs 删除"); err != nil {
		t.Fatalf("note rm --stage 裁决应成功: %v", err)
	}
	cr, err := a.MergeContinue(ctx, "merge theirs:p 接受删除")
	if err != nil {
		t.Fatalf("continue 应成功: %v", err)
	}
	if cr.Resolved != 1 {
		t.Fatalf("应应用 1 条裁决,got %d", cr.Resolved)
	}
	if _, err := a.Note(ctx, "p"); err == nil {
		t.Fatal("接受删除裁决后 p 不应存在")
	}
	fk, err := a.FSCK(ctx)
	if err != nil || len(fk.Problems) != 0 {
		t.Fatalf("收束后 fsck 应零问题: %+v err=%v", fk, err)
	}
}
