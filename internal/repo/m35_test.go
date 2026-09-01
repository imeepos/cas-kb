package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

func TestM35_ProjectsIsolated(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	for _, p := range []string{"alpha", "beta"} {
		if err := s.ProjectCreate(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	ra := Open(s, Config{Project: "alpha", Now: func() int64 { return fixedTime }})
	rb := Open(s, Config{Project: "beta", Now: func() int64 { return fixedTime }})
	if _, _, err := ra.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rb.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b"); err != nil {
		t.Fatal(err)
	}
	na, err := ra.ListNotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(na) != 1 || na[0].Slug != "a" {
		t.Fatalf("alpha 应只看到 a,got %v", na)
	}
	nb, err := rb.ListNotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 1 || nb[0].Slug != "b" {
		t.Fatalf("beta 应只看到 b,got %v", nb)
	}
	if _, err := rb.Note(ctx, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("beta 不应看到 alpha 的条目,got %v", err)
	}
	if _, err := ra.Note(ctx, "b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("alpha 不应看到 beta 的条目,got %v", err)
	}
}

func TestM35_DefaultProjectUnspecified(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	rd := Open(s, Config{Now: func() int64 { return fixedTime }})
	if rd.project != "default" {
		t.Fatalf("未指定项目应落 default,got %q", rd.project)
	}
	if _, _, err := rd.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BranchGet(ctx, "default", "main"); err != nil {
		t.Fatalf("default 项目应可读 main 分支: %v", err)
	}
}

func TestM35_UnknownProjectFailsLoud(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	r := Open(s, Config{Project: "ghost", Now: func() int64 { return fixedTime }})
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a"); err == nil {
		t.Fatal("不存在的项目应响亮失败(外键约束)")
	}
}
