package repo

import (
	"context"
	"fmt"
	"testing"
)

// M4 压测根治路径:批量导入单快照、索引一次增量。
func TestBulkImport(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "bulkmain")
	items := make([]BulkInput, 0, 300)
	for i := 1; i <= 300; i++ {
		items = append(items, BulkInput{
			Path: fmt.Sprintf("t%d/n%d", i%8, i),
			In:   NoteInput{Title: fmt.Sprintf("B%d channel", i), Body: fmt.Sprintf("第 %d 条 bulk 内容,讨论 channel。", i), Tags: []string{"bulk"}},
		})
	}
	snap, n, err := r.BulkImport(ctx, items, "bulk 300")
	if err != nil {
		t.Fatal(err)
	}
	if n != 300 {
		t.Fatalf("导入数不符: %d", n)
	}
	// 单快照:历史深度应为 1
	log, err := r.Log(ctx)
	if err != nil || len(log) != 1 {
		t.Fatalf("批量导入应只产生一个快照: %d %v", len(log), err)
	}
	refs, err := r.ListNotes(ctx, "")
	if err != nil || len(refs) != 300 {
		t.Fatalf("条目数不符: %d %v", len(refs), err)
	}
	hits, err := r.Search(ctx, "channel", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 300 {
		t.Fatalf("search 应命中 300: %d", len(hits))
	}
	// --at 用头快照(即唯一快照)检索
	atHits, err := r.Search(ctx, "channel", string(snap))
	if err != nil || len(atHits) != 300 {
		t.Fatalf("--at 检索不符: %d %v", len(atHits), err)
	}
}

// 与已有单条数据混合:批量增量不得破坏旧词检索,路径更新以最后一条为准。
func TestBulkImportMixedWithExisting(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "bulkmix")
	if _, _, err := r.SetNote(ctx, "old", NoteInput{Title: "Old 遗留", Body: "legacy 旧词"}, "add old"); err != nil {
		t.Fatal(err)
	}
	items := []BulkInput{{Path: "old", In: NoteInput{Title: "Old 更新版", Body: "legacy 旧词 新词fresh"}}}
	for i := 1; i <= 100; i++ {
		items = append(items, BulkInput{Path: fmt.Sprintf("g%d", i), In: NoteInput{Title: fmt.Sprintf("G%d fresh", i), Body: "fresh 内容"}})
	}
	if _, n, err := r.BulkImport(ctx, items, "bulk mix"); err != nil || n != 101 {
		t.Fatalf("bulk mix: %d %v", n, err)
	}
	refs, err := r.ListNotes(ctx, "")
	if err != nil || len(refs) != 101 {
		t.Fatalf("总条数应 101: %d %v", len(refs), err)
	}
	oldHits, err := r.Search(ctx, "legacy", "")
	if err != nil || len(oldHits) != 1 {
		t.Fatalf("legacy 应命中 1: %d %v", len(oldHits), err)
	}
	freshHits, err := r.Search(ctx, "fresh", "")
	if err != nil || len(freshHits) != 101 {
		t.Fatalf("fresh 应命中 101: %d %v", len(freshHits), err)
	}
}

// 错误路径:空导入/空标题/条目挡路。
func TestBulkImportErrors(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "bulkerr")
	if _, _, err := r.BulkImport(ctx, nil, "empty"); err == nil {
		t.Fatal("空导入应报错")
	}
	if _, _, err := r.BulkImport(ctx, []BulkInput{{Path: "a", In: NoteInput{Body: "no title"}}}, "x"); err == nil {
		t.Fatal("空标题应报错")
	}
	if _, _, err := r.BulkImport(ctx, []BulkInput{{Path: "c", In: NoteInput{Title: "C", Body: "b"}}}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.BulkImport(ctx, []BulkInput{{Path: "c/sub", In: NoteInput{Title: "Y", Body: "b"}}}, "x"); err == nil {
		t.Fatal("条目挡路应报错")
	}
}
