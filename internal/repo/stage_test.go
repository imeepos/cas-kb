package repo

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// 暂存工作流主干:stage 三条 → main 不可见 → commit 单快照合入 → stage 归零。
func TestStageWorkflowCommit(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "stagewf")
	for i := 1; i <= 3; i++ {
		if _, _, err := r.StageNote(ctx, fmt.Sprintf("g%d", i), NoteInput{Title: fmt.Sprintf("G%d fresh", i), Body: "fresh 内容"}, "s"); err != nil {
			t.Fatal(err)
		}
	}
	// 暂存对 main 不可见
	refs, err := r.ListNotes(ctx, "")
	if err != nil || len(refs) != 0 {
		t.Fatalf("commit 前 main 应为空: %d %v", len(refs), err)
	}
	log, err := r.Log(ctx)
	if err != nil || len(log) != 0 {
		t.Fatalf("暂存不产生正式提交: %d %v", len(log), err)
	}
	st, err := r.StageStatus(ctx)
	if err != nil || len(st) != 3 {
		t.Fatalf("状态应 3 条: %+v %v", st, err)
	}
	for _, c := range st {
		if c.Op != "A" {
			t.Fatalf("新增应为 A: %+v", c)
		}
	}
	// 提交
	snap, applied, err := r.CommitStage(ctx, "commit 3")
	if err != nil || applied != 3 {
		t.Fatalf("commit: %d %v", applied, err)
	}
	refs, err = r.ListNotes(ctx, "")
	if err != nil || len(refs) != 3 {
		t.Fatalf("提交后应 3 条: %d %v", len(refs), err)
	}
	hits, err := r.Search(ctx, "fresh", "")
	if err != nil || len(hits) != 3 {
		t.Fatalf("提交后检索应命中 3: %d %v", len(hits), err)
	}
	log, err = r.Log(ctx)
	if err != nil || len(log) != 1 {
		t.Fatalf("提交应只产生一个快照: %d %v", len(log), err)
	}
	// stage 分支已清理
	if _, err := r.st.BranchGet(ctx, r.project, r.stageBranchName()); err == nil {
		t.Fatal("stage 分支应已删除")
	}
	// 再次 commit:无暂存
	if _, _, err := r.CommitStage(ctx, "x"); err == nil || !strings.Contains(err.Error(), "没有暂存内容") {
		t.Fatalf("无暂存应报错: %v", err)
	}
	_ = snap
}

// main 在暂存期间前进:commit 以暂存为准覆盖同名路径,main 其他变更保留。
func TestStageCommitWithMainAdvance(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "stagemix")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A v1", Body: "v1"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "c", NoteInput{Title: "C v1", Body: "cv1"}, "add c"); err != nil {
		t.Fatal(err)
	}
	// 暂存:更新 a + 新增 b
	if _, _, err := r.StageNote(ctx, "a", NoteInput{Title: "A v2", Body: "v2 stage"}, "stage a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.StageNote(ctx, "b", NoteInput{Title: "B", Body: "b body"}, "stage b"); err != nil {
		t.Fatal(err)
	}
	// main 前进:非暂存写入 c2
	if _, _, err := r.SetNote(ctx, "c", NoteInput{Title: "C v2", Body: "cv2"}, "update c"); err != nil {
		t.Fatal(err)
	}
	if _, applied, err := r.CommitStage(ctx, "commit stage"); err != nil || applied != 2 {
		t.Fatalf("commit: %d %v", applied, err)
	}
	aref, err := r.Note(ctx, "a")
	if err != nil || aref.Note.Meta.Title != "A v2" {
		t.Fatalf("a 应为暂存版: %+v", aref)
	}
	cref, err := r.Note(ctx, "c")
	if err != nil || cref.Note.Meta.Title != "C v2" {
		t.Fatalf("main 上 c 的前进应保留: %+v", cref)
	}
	if _, err := r.Note(ctx, "b"); err != nil {
		t.Fatalf("b 应存在: %v", err)
	}
}

// 删除暂存与 abort。
func TestStageRemoveAndAbort(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "stageabort")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "x"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Body: "y"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StageRemoveNote(ctx, "a", "stage rm a"); err != nil {
		t.Fatal(err)
	}
	st, err := r.StageStatus(ctx)
	if err != nil || len(st) != 1 || st[0].Op != "D" || st[0].Path != "a" {
		t.Fatalf("删除暂存状态不符: %+v %v", st, err)
	}
	if _, applied, err := r.CommitStage(ctx, "commit rm"); err != nil || applied != 1 {
		t.Fatalf("commit: %d %v", applied, err)
	}
	refs, _ := r.ListNotes(ctx, "")
	if len(refs) != 1 || refs[0].Path != "b" {
		t.Fatalf("删除后应仅剩 b: %+v", refs)
	}
	// abort
	if _, _, err := r.StageNote(ctx, "z", NoteInput{Title: "Z", Body: "z"}, "stage z"); err != nil {
		t.Fatal(err)
	}
	if err := r.AbortStage(ctx); err != nil {
		t.Fatal(err)
	}
	refs, _ = r.ListNotes(ctx, "")
	if len(refs) != 1 {
		t.Fatalf("abort 后应仍 1 条: %d", len(refs))
	}
	if _, _, err := r.CommitStage(ctx, "x"); err == nil || !strings.Contains(err.Error(), "没有暂存内容") {
		t.Fatalf("abort 后 commit 应报无暂存: %v", err)
	}
}
