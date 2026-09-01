package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
)

// M4 契约固化(复盘 P0):search 对「空库」与「无索引快照」的行为必须确定——
// 空库(无任何提交)→ 无结果;有提交但快照无索引 → 报错并指引 rebuild。
func TestM4_SearchContract(t *testing.T) {
	ctx := context.Background()
	r, st, _ := newRepo(t, "m4_search_contract")

	// 1) 空库(无分支头):检索返回空,不报错——与 note ls 的「(no notes)」对齐
	hits, err := r.Search(ctx, "anything", "")
	if err != nil {
		t.Fatalf("空库检索不应报错: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("空库检索应无结果: %+v", hits)
	}

	// 2) 有提交但快照无索引(旧数据形态):报错并指引 rebuild
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "body of a"}, "add a"); err != nil {
		t.Fatal(err)
	}
	head, has, err := r.head(ctx)
	if err != nil || !has {
		t.Fatalf("应有分支头: %v", err)
	}
	snap, err := r.loadSnapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	// 构造无索引快照:复制头快照,清空 index 字段(模拟 M4 之前的旧数据),推进分支
	legacy := *snap
	legacy.Index = ""
	legacyData, err := object.EncodeSnapshot(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyAddr, err := st.Put(ctx, object.KindSnapshot, legacyData)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BranchSet(ctx, r.Project(), r.Branch(), legacyAddr); err != nil {
		t.Fatal(err)
	}
	_, err = r.Search(ctx, "anything", "")
	if err == nil {
		t.Fatal("无索引快照检索应报错")
	}
	if !strings.Contains(err.Error(), "index rebuild") {
		t.Fatalf("报错应指引 rebuild: %v", err)
	}
}
