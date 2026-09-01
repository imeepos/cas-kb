package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

func TestM36_ResetMovesHeadAndCounts(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m36_reset")
	c1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "c", NoteInput{Title: "C"}, "add c"); err != nil {
		t.Fatal(err)
	}
	res, err := r.Reset(ctx, string(c1)[:16]) // 短标识回退
	if err != nil {
		t.Fatal(err)
	}
	if res.Abandoned != 2 {
		t.Fatalf("应放弃 2 个提交,got %d", res.Abandoned)
	}
	logs, err := r.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Addr != c1 {
		t.Fatalf("回退后头应为 c1: %v", logs)
	}
	if _, err := r.Note(ctx, "b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("被放弃的条目 b 不应再可见: %v", err)
	}
}

func TestM36_ResetRejectsNonAncestor(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	for _, p := range []string{"alpha", "beta"} {
		if err := s.ProjectCreate(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	ra := Open(s, Config{Project: "alpha", Now: func() int64 { return fixedTime }})
	rbo := Open(s, Config{Project: "beta", Now: func() int64 { return fixedTime }})
	if _, _, err := ra.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a"); err != nil {
		t.Fatal(err)
	}
	_, cb, err := rbo.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ra.Reset(ctx, string(cb)); !errors.Is(err, ErrResetTargetNotAncestor) {
		t.Fatalf("他项目快照应被拒绝回退,got %v", err)
	}
}

func TestM36_ResetNoOpAtHead(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m36_noop")
	c1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Reset(ctx, string(c1)[:16])
	if err != nil {
		t.Fatal(err)
	}
	if res.Abandoned != 0 || res.From != c1 || res.To != c1 {
		t.Fatalf("回退到当前头应为空操作: %+v", res)
	}
}

func TestM36_ResetOnEmptyBranchFails(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m36_empty")
	if _, err := r.Reset(ctx, "main"); err == nil {
		t.Fatal("空分支应无可回退")
	}
}
