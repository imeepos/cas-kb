package repo

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// gc --keep-last:深度 >= K 的历史快照仅精简检索索引——数据本体(树/笔记/
// 正文)保留可读,head 检索不受影响,fsck 按水印豁免后保持干净。
func TestGCKeepLast(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "keeplast")
	var firstSnap string
	for i := 1; i <= 30; i++ {
		snap, _, err := r.SetNote(ctx, fmt.Sprintf("n%d", i), NoteInput{Title: fmt.Sprintf("H%d", i), Body: fmt.Sprintf("body %d", i)}, "h")
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			firstSnap = string(snap)
		}
	}
	// keep=10:最近 10 个快照(含 head)保留索引,更早精简
	if _, err := r.GCWithKeepLast(ctx, 10); err != nil {
		t.Fatal(err)
	}
	// head 检索可用
	hits, err := r.Search(ctx, "H30", "")
	if err != nil || len(hits) != 1 {
		t.Fatalf("head 检索应可用: %d %v", len(hits), err)
	}
	// 数据本体保留:最旧快照的条目仍可按 --at 读取
	if _, err := r.NoteAt(ctx, "n1", firstSnap); err != nil {
		t.Fatalf("历史数据应保留: %v", err)
	}
	// 但被精简快照的检索应报友好错误
	_, err = r.Search(ctx, "H1", firstSnap)
	if err == nil || !strings.Contains(err.Error(), "已被 gc 精简") {
		t.Fatalf("精简快照检索应报友好错误: %v", err)
	}
	// fsck:水位豁免后保持干净
	fsckRes, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fsckRes.Problems) != 0 {
		t.Fatalf("fsck 应无问题: %+v", fsckRes.Problems)
	}
	// 普通 GC 再跑(水印仍在):不报错,fsck 依旧干净
	if _, err := r.GC(ctx); err != nil {
		t.Fatal(err)
	}
	fsckRes, err = r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fsckRes.Problems) != 0 {
		t.Fatalf("二次 fsck 应无问题: %+v", fsckRes.Problems)
	}
}

// keep-last 边界:K 覆盖全部历史时等价于不精简。
func TestGCKeepLastCoversAll(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "keeplastall")
	for i := 1; i <= 5; i++ {
		if _, _, err := r.SetNote(ctx, fmt.Sprintf("n%d", i), NoteInput{Title: fmt.Sprintf("N%d", i), Body: "b"}, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.GCWithKeepLast(ctx, 100); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		hits, err := r.Search(ctx, fmt.Sprintf("N%d", i), "")
		if err != nil || len(hits) != 1 {
			t.Fatalf("N%d 检索: %d %v", i, len(hits), err)
		}
	}
}
