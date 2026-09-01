package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/store"
)

// setupBranches 建 br_main(两个快照)与 br_dev(指回第一快照)。
func setupBranches(t *testing.T) (*Repo, hash.Address, hash.Address) {
	ctx := context.Background()
	r, s, _ := newRepo(t, "br_main")
	c1, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "br_dev", c1); err != nil {
		t.Fatal(err)
	}
	return r, c1, c2
}

func TestM2_DiffAcrossBranchesByName(t *testing.T) {
	r, _, _ := setupBranches(t)
	ctx := context.Background()
	removed, err := r.Diff(ctx, "br_main", "br_dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Slug != "b" || removed[0].Type != ChangeRemoved {
		t.Fatalf("br_main->br_dev 应只有 b removed,got %v", removed)
	}
	added, err := r.Diff(ctx, "br_dev", "br_main")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Slug != "b" || added[0].Type != ChangeAdded {
		t.Fatalf("br_dev->br_main 应只有 b added,got %v", added)
	}
}

func TestM2_DiffByShortID(t *testing.T) {
	r, c1, c2 := setupBranches(t)
	ctx := context.Background()
	base := string(c1)[:16] // kb log 输出的短标识长度
	tip := string(c2)[:16]
	changes, err := r.Diff(ctx, base, tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Slug != "b" || changes[0].Type != ChangeAdded {
		t.Fatalf("短标识 diff 应只有 b added,got %v", changes)
	}
}

func TestM2_DiffShortIDAmbiguous(t *testing.T) {
	r, _, c2 := setupBranches(t)
	ctx := context.Background()
	if _, err := r.Diff(ctx, "sha256:", string(c2)); !errors.Is(err, ErrAmbiguousRef) {
		t.Fatalf("过短标识应报歧义错误,got %v", err)
	}
}

func TestM2_DiffShortIDNoMatch(t *testing.T) {
	r, c1, _ := setupBranches(t)
	ctx := context.Background()
	prefix := mismatchPrefix(string(c1))
	if _, err := r.Diff(ctx, prefix, string(c1)); !errors.Is(err, store.ErrBranchNotFound) {
		t.Fatalf("无匹配短标识应报未找到,got %v", err)
	}
}

// mismatchPrefix 构造一个确定不匹配任何快照的地址前缀。
func mismatchPrefix(addr string) string {
	tail := "ff"
	if addr[68:70] == "ff" {
		tail = "00"
	}
	return addr[:68] + tail
}
