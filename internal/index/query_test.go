package index

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/testdb"
)

func mkAddr(hex string) hash.Address { return hash.Address("sha256:" + hex) }

// putShard 写入分片对象并返回地址(零依赖 SQLite)。
func putShard(t *testing.T, st store.Store, shard *object.IndexShard) hash.Address {
	t.Helper()
	data, err := object.EncodeIndexShard(shard)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := st.Put(context.Background(), object.KindIndexShard, data)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// slotRoot 构造只有一个非空分片的索引根。
func slotRoot(t *testing.T, st store.Store, shard *object.IndexShard, docs []object.IndexDoc) *object.IndexRoot {
	t.Helper()
	addr := putShard(t, st, shard)
	slots := make([]hash.Address, ShardCount)
	slots[shard.Bucket] = addr
	return &object.IndexRoot{Kind: object.KindIndexRoot, Version: IndexVersion, Shards: slots, Docs: docs}
}

// M4:空查询与空索引——不命中、不报错,且两次查询结果一致(确定性)。
func TestSearchEmptyAndDeterministic(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	empty := &object.IndexRoot{Kind: object.KindIndexRoot, Version: IndexVersion, Shards: make([]hash.Address, ShardCount)}
	h1, err := Search(ctx, st, empty, "anything")
	if err != nil || len(h1) != 0 {
		t.Fatalf("空索引应无命中: %v %v", h1, err)
	}
	h2, _ := Search(ctx, st, empty, "anything")
	if !reflect.DeepEqual(h1, h2) {
		t.Fatal("同一索引两次查询应一致")
	}
	if h3, _ := Search(ctx, st, empty, "  。,"); len(h3) != 0 {
		t.Fatal("纯分隔符查询应无命中")
	}
}

// M4:标题命中应排在仅正文命中之前(字段加权),分数严格更高。
func TestSearchTitleBeatsBody(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	noteA, noteB := mkAddr(strings.Repeat("ab", 32)), mkAddr(strings.Repeat("cd", 32))
	shard := &object.IndexShard{
		Kind: object.KindIndexShard, Bucket: BucketOf("channel"),
		Postings: map[string][]object.IndexPosting{
			"channel": {
				{Addr: noteA, Title: 1},
				{Addr: noteB, Body: 1},
			},
		},
	}
	root := slotRoot(t, st, shard, []object.IndexDoc{
		{Addr: noteA, Path: "a", Len: 3},
		{Addr: noteB, Path: "b", Len: 3},
	})
	hits, err := Search(ctx, st, root, "channel")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Path != "a" || hits[0].Score <= hits[1].Score {
		t.Fatalf("标题命中应靠前且分数更高: %+v", hits)
	}
}

// M4:多词都命中的文档应排在只命中一词者之前。
func TestSearchMultiTermBeatsPartial(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	both, partial := mkAddr(strings.Repeat("11", 32)), mkAddr(strings.Repeat("22", 32))
	b1 := BucketOf("go")
	sh1 := &object.IndexShard{Kind: object.KindIndexShard, Bucket: b1, Postings: map[string][]object.IndexPosting{
		"go": {{Addr: both, Body: 1}, {Addr: partial, Body: 1}},
	}}
	b2 := BucketOf("channel")
	sh2 := &object.IndexShard{Kind: object.KindIndexShard, Bucket: b2, Postings: map[string][]object.IndexPosting{
		"channel": {{Addr: both, Body: 1}},
	}}
	slots := make([]hash.Address, ShardCount)
	slots[b1] = putShard(t, st, sh1)
	slots[b2] = putShard(t, st, sh2)
	root := &object.IndexRoot{Kind: object.KindIndexRoot, Version: IndexVersion, Shards: slots, Docs: []object.IndexDoc{
		{Addr: both, Path: "both", Len: 2},
		{Addr: partial, Path: "partial", Len: 1},
	}}
	hits, err := Search(ctx, st, root, "go channel")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Path != "both" {
		t.Fatalf("双词命中应靠前: %+v", hits)
	}
}
