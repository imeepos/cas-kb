package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/index"
	"github.com/imeepos/cas-kb/internal/object"
)

// loadHeadIndex 载入当前分支头的快照与索引根。
func loadHeadIndex(t *testing.T, r *Repo) (*object.Snapshot, *object.IndexRoot) {
	t.Helper()
	ctx := context.Background()
	head, has, err := r.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("应有分支头")
	}
	snap, err := r.loadSnapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index == "" {
		t.Fatal("快照应携带索引根地址")
	}
	root, err := index.LoadRoot(ctx, r.st, snap.Index)
	if err != nil {
		t.Fatal(err)
	}
	return snap, root
}

// bucketsOf 计算标题+正文词元的桶号集合(测试辅助)。
func bucketsOf(title, body string) map[int]bool {
	out := map[int]bool{}
	for _, term := range index.Tokenize(title + " " + body) {
		out[index.BucketOf(term.Text)] = true
	}
	return out
}

// M4:提交即建索引,索引随快照可达(GC 保留、fsck 通过)。
func TestM4_IndexBuiltOnCommit(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_index_build")
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel 并发", Body: "chan 语义"}, "add channel"); err != nil {
		t.Fatal(err)
	}
	snap, root := loadHeadIndex(t, r)
	if len(root.Docs) != 1 || root.Docs[0].Path != "go/channel" {
		t.Fatalf("文档表不符: %+v", root.Docs)
	}
	shard, err := index.LoadShard(ctx, r.st, root.Shards[index.BucketOf("channel")])
	if err != nil {
		t.Fatal(err)
	}
	postings, ok := shard.Postings["channel"]
	if !ok || len(postings) != 1 || postings[0].Title != 1 {
		t.Fatalf("channel 倒排项不符: %+v", postings)
	}
	if postings[0].Addr != root.Docs[0].Addr {
		t.Fatal("倒排项应指向文档表中的笔记")
	}
	if _, err := r.GC(ctx); err != nil {
		t.Fatal(err)
	}
	fsckRes, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fsckRes.Problems) != 0 {
		t.Fatalf("fsck 应无问题: %+v", fsckRes.Problems)
	}
	if _, err := index.LoadRoot(ctx, r.st, snap.Index); err != nil {
		t.Fatalf("GC 后索引应随快照保留: %v", err)
	}
}

// M4:增量重建结构共享——只改一篇笔记,仅其词元所在桶分片地址变化;
// 纯目录提交(叶子集不变)索引地址原样复用。
func TestM4_IndexStructuralSharing(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_index_share")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "Alpha 内容", Body: "alpha body"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "Beta 标签", Body: "beta body"}, "add b"); err != nil {
		t.Fatal(err)
	}
	_, root2 := loadHeadIndex(t, r)

	// 1) 新增 c:非 c 词元桶的地址必须逐一相同
	if _, _, err := r.SetNote(ctx, "c", NoteInput{Title: "Gamma", Body: "gamma unique"}, "add c"); err != nil {
		t.Fatal(err)
	}
	snap3, root3 := loadHeadIndex(t, r)
	cBuckets := bucketsOf("Gamma", "gamma unique")
	changed := 0
	for b := 0; b < index.ShardCount; b++ {
		if root2.Shards[b] != root3.Shards[b] {
			changed++
			if !cBuckets[b] {
				t.Fatalf("桶 %d 不含 c 的词元却变化: %s -> %s", b, root2.Shards[b], root3.Shards[b])
			}
		}
	}
	if changed == 0 || len(root3.Docs) != 3 {
		t.Fatalf("新增 c 应改动若干桶并更新文档表: changed=%d docs=%d", changed, len(root3.Docs))
	}

	// 2) 纯目录提交:叶子集与内容不变 → 索引地址原样复用(与 commit3 相同)
	if _, _, err := r.Mkdir(ctx, "subdir", "add dir"); err != nil {
		t.Fatal(err)
	}
	snap4, root4 := loadHeadIndex(t, r)
	if snap4.Index != snap3.Index {
		t.Fatalf("纯目录提交索引地址应复用: %s != %s", snap4.Index, snap3.Index)
	}
	if len(root4.Docs) != 3 {
		t.Fatalf("文档表应仍为 3 项: %d", len(root4.Docs))
	}
}

// M4:删除笔记后其倒排项消失;RebuildIndex 全量重建结果与增量一致。
func TestM4_IndexRemoveAndRebuild(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_index_rebuild")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "Alpha 内容", Body: "alpha body"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "Beta 标签", Body: "beta body"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveNote(ctx, "a", "rm a"); err != nil {
		t.Fatal(err)
	}
	_, root := loadHeadIndex(t, r)
	if len(root.Docs) != 1 || root.Docs[0].Path != "b" {
		t.Fatalf("删除后文档表应仅剩 b: %+v", root.Docs)
	}
	for _, doc := range root.Docs {
		if doc.Path == "a" {
			t.Fatal("a 的文档行应删除")
		}
	}
	// alpha 词元的倒排项应消失
	shard, err := index.LoadShard(ctx, r.st, root.Shards[index.BucketOf("alpha")])
	if err != nil {
		t.Fatal(err)
	}
	if shard != nil {
		if _, ok := shard.Postings["alpha"]; ok {
			t.Fatal("alpha 倒排项应已删除")
		}
	}
	// 全量重建:快照推进,头快照指向重建出的索引根
	snapAddr, rootAddr, err := r.RebuildIndex(ctx, "index rebuild")
	if err != nil {
		t.Fatal(err)
	}
	snapH, root2 := loadHeadIndex(t, r)
	if snapH.Index != rootAddr {
		t.Fatalf("重建后头快照索引应为新根: %s != %s", snapH.Index, rootAddr)
	}
	if len(root2.Docs) != 1 {
		t.Fatalf("重建后文档表应仍 1 项: %d", len(root2.Docs))
	}
	head, has, err := r.head(ctx)
	if err != nil || !has {
		t.Fatalf("应有分支头: %v", err)
	}
	if head != snapAddr {
		t.Fatal("重建后分支头应指向新快照")
	}
}
