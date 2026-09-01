package repo

import (
	"context"
	"reflect"
	"testing"
)

// M4:repo 层检索——字段加权排序、确定性、--at 历史快照检索。
func TestM4_Search(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_search")
	c1, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel 并发", Body: "chan 语义"}, "add channel")
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := r.SetNote(ctx, "web/only", NoteInput{Title: "Only", Body: "唯一正文提及 channel"}, "add only")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "misc/other", NoteInput{Title: "Other", Body: "别的"}, "add other"); err != nil {
		t.Fatal(err)
	}

	// 命中两篇;标题命中(Channel)应排在正文命中之前
	hits, err := r.Search(ctx, "channel", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Path != "go/channel" {
		t.Fatalf("检索结果不符: %+v", hits)
	}
	// 确定性:同快照同查询两次一致
	hits2, err := r.Search(ctx, "channel", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hits, hits2) {
		t.Fatal("同快照同查询应逐项一致(ROADMAP M4)")
	}
	// --at 历史快照:c1 时刻 web/only 不存在
	oldHits, err := r.Search(ctx, "唯一", string(c1))
	if err != nil {
		t.Fatal(err)
	}
	if len(oldHits) != 0 {
		t.Fatalf("c1 快照不应命中 web/only: %+v", oldHits)
	}
	curHits, err := r.Search(ctx, "唯一", string(c2))
	if err != nil {
		t.Fatal(err)
	}
	if len(curHits) != 1 || curHits[0].Path != "web/only" {
		t.Fatalf("c2 快照应命中 web/only: %+v", curHits)
	}
	// 无命中
	if none, _ := r.Search(ctx, "完全不存在xyz", ""); len(none) != 0 {
		t.Fatalf("不应命中: %+v", none)
	}
}
