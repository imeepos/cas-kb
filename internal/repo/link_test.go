package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
)

func TestM2_LinkCrossVersionSelfConsistent(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_links")
	_, addrA1, err := r.SetNote(ctx, "a", NoteInput{Title: "A v1", Body: "old body"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	c1, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Links: []object.Link{{Slug: "a"}}}, "add b links a")
	if err != nil {
		t.Fatal(err)
	}
	c2, addrA2, err := r.SetNote(ctx, "a", NoteInput{Title: "A v2", Body: "new body"}, "modify a")
	if err != nil {
		t.Fatal(err)
	}
	if addrA1 == addrA2 {
		t.Fatal("修改后 A 的地址应变化")
	}
	// 旧快照 c1 解析 a -> 旧 A;新快照 c2 解析 a -> 新 A
	treeOld, err := r.treeAtSnapshot(ctx, c1)
	if err != nil {
		t.Fatal(err)
	}
	treeNew, err := r.treeAtSnapshot(ctx, c2)
	if err != nil {
		t.Fatal(err)
	}
	aOld, ok := treeOld.Get("a")
	if !ok || aOld != addrA1 {
		t.Fatalf("旧快照应解析到旧 A:%v != %v", aOld, addrA1)
	}
	aNew, ok := treeNew.Get("a")
	if !ok || aNew != addrA2 {
		t.Fatalf("新快照应解析到新 A:%v != %v", aNew, addrA2)
	}
	// B 在两个快照中地址一致(未变),其链接指向的 slug 在两个快照中分别解析到新旧 A
	bOld, _ := treeOld.Get("b")
	bNew, _ := treeNew.Get("b")
	if bOld != bNew {
		t.Fatalf("B 未修改,地址应一致: %v != %v", bOld, bNew)
	}
}

func TestM2_RemoveSlugAffectsOnlyNewSnapshot(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_remove")
	c1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b")
	if err != nil {
		t.Fatal(err)
	}
	c3, err := r.RemoveNote(ctx, "a", "rm a")
	if err != nil {
		t.Fatal(err)
	}
	tree1, _ := r.treeAtSnapshot(ctx, c1)
	tree2, _ := r.treeAtSnapshot(ctx, c2)
	tree3, _ := r.treeAtSnapshot(ctx, c3)
	if _, ok := tree1.Get("a"); !ok {
		t.Fatal("旧快照应仍含 a")
	}
	if _, ok := tree2.Get("a"); !ok {
		t.Fatal("删除前快照应仍含 a")
	}
	if _, ok := tree3.Get("a"); ok {
		t.Fatal("新快照不应含 a")
	}
	if _, ok := tree3.Get("b"); !ok {
		t.Fatal("新快照应仍含 b")
	}
}
