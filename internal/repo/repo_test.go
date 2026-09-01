package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

const fixedTime = int64(1700000000)

func newRepo(t *testing.T, branch string) (*Repo, *store.PG, string) {
	dsn := freshDB(t)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := Open(s, Config{Branch: branch, Now: func() int64 { return fixedTime }})
	return r, s, dsn
}

func TestM2_NoteRoundtrip(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_roundtrip")
	_, _, err := r.SetNote(ctx, "go", NoteInput{
		Title: "Go 入门", Body: "正文内容", Tags: []string{"go", "编程"},
		Links: []object.Link{{Slug: "design", Rel: "ref"}},
	}, "add go")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Note(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Note.Meta.Title != "Go 入门" || string(ref.Body) != "正文内容" {
		t.Fatalf("标题/正文不一致: %+v", ref)
	}
	if len(ref.Note.Meta.Tags) != 2 || ref.Note.Meta.Tags[0] != "go" {
		t.Fatalf("tags 不一致: %v", ref.Note.Meta.Tags)
	}
	if len(ref.Note.Links) != 1 || ref.Note.Links[0].Slug != "design" {
		t.Fatalf("links 不一致: %v", ref.Note.Links)
	}
}

func TestM2_LogParentsChain(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_log")
	s1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "body a"}, "first")
	if err != nil {
		t.Fatal(err)
	}
	s2, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Body: "body b"}, "second")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := r.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("期望 2 个快照,got %d", len(logs))
	}
	if logs[0].Addr != s2 || logs[1].Addr != s1 {
		t.Fatalf("日志顺序或地址错误: %v %v", logs[0].Addr, s1)
	}
	if len(logs[0].Snapshot.Parents) != 1 || logs[0].Snapshot.Parents[0] != s1 {
		t.Fatalf("parents 链错误: %v", logs[0].Snapshot.Parents)
	}
	if len(logs[1].Snapshot.Parents) != 0 {
		t.Fatalf("首快照不应有 parent: %v", logs[1].Snapshot.Parents)
	}
}

func setupDiffScenario(t *testing.T) (*Repo, hash.Address, hash.Address, hash.Address, hash.Address) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_diff")
	c1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A v1"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b")
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A v2"}, "modify a")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.SetNote(ctx, "c", NoteInput{Title: "C"}, "add c")
	if err != nil {
		t.Fatal(err)
	}
	c3, err := r.RemoveNote(ctx, "b", "rm b")
	if err != nil {
		t.Fatal(err)
	}
	c4, err := r.RemoveNote(ctx, "c", "rm c")
	if err != nil {
		t.Fatal(err)
	}
	return r, c1, c2, c3, c4
}

func TestM2_DiffOnlyUpdated(t *testing.T) {
	r, c1, _, _, c4 := setupDiffScenario(t)
	ctx := context.Background()
	changes, err := r.Diff(ctx, string(c1), string(c4))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Slug != "a" || changes[0].Type != ChangeUpdated {
		t.Fatalf("c1->c4 应只有 a updated,got %v", changes)
	}
}

func TestM2_DiffRemovedAndAdded(t *testing.T) {
	r, _, c2, c3, _ := setupDiffScenario(t)
	ctx := context.Background()
	changes, err := r.Diff(ctx, string(c2), string(c3))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ch := range changes {
		got[ch.Slug] = string(ch.Type)
	}
	if got["b"] != string(ChangeRemoved) || got["c"] != string(ChangeAdded) {
		t.Fatalf("应含 b removed 与 c added,got %v", got)
	}
	if _, ok := got["a"]; ok {
		t.Fatalf("a 未变化不应出现在 diff,got %v", got)
	}
}
