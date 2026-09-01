package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// freshStore 打开一个独立测试库,返回存储与其 DSN。
func freshStore(t *testing.T) (*store.PG, string) {
	dsn := freshDB(t)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dsn
}

// seedOrphanAndNote 写入一个孤儿对象和一篇可达笔记,返回孤儿地址。
func seedOrphanAndNote(t *testing.T, ctx context.Context, r *Repo, s *store.PG) hash.Address {
	t.Helper()
	orphan, err := s.Put(ctx, object.KindBlob, []byte("gc-protect orphan body"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "keep a body"}, "a"); err != nil {
		t.Fatal(err)
	}
	return orphan
}

func TestM3_GCProtectExportsBranches(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	var orphan hash.Address
	var backups [][]store.BranchRef
	r := Open(s, Config{
		Branch:    "m3_gc_protect_on",
		Now:       func() int64 { return fixedTime },
		GCProtect: true,
		GCExportBranches: func(_ context.Context, bs []store.BranchRef) error {
			if has, _ := s.Has(ctx, orphan); !has {
				t.Error("分支表备份必须在清扫之前执行")
			}
			backups = append(backups, bs)
			return nil
		},
	})
	orphan = seedOrphanAndNote(t, ctx, r, s)
	res, err := r.GC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("保护开启应恰好备份一次,got %d", len(backups))
	}
	var found bool
	for _, b := range backups[0] {
		if b.Name == "m3_gc_protect_on" && b.Addr != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("备份应包含分支头,got %v", backups[0])
	}
	if res.Swept == 0 {
		t.Fatal("备份后 GC 仍应清扫不可达对象")
	}
	if has, _ := s.Has(ctx, orphan); has {
		t.Fatal("孤儿对象应被清扫")
	}
}

func TestM3_GCProtectDisabledSkipsExport(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	calls := 0
	r := Open(s, Config{
		Branch: "m3_gc_protect_off",
		Now:    func() int64 { return fixedTime },
		GCExportBranches: func(_ context.Context, _ []store.BranchRef) error {
			calls++
			return nil
		},
	})
	orphan := seedOrphanAndNote(t, ctx, r, s)
	if _, err := r.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("保护关闭时应跳过分支表备份,实际调用 %d 次", calls)
	}
	if has, _ := s.Has(ctx, orphan); has {
		t.Fatal("保护关闭时 GC 仍应清扫孤儿对象")
	}
}

func TestM3_GCProtectWithoutExportAborts(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	r := Open(s, Config{Branch: "m3_gc_protect_nofn", Now: func() int64 { return fixedTime }, GCProtect: true})
	orphan := seedOrphanAndNote(t, ctx, r, s)
	if _, err := r.GC(ctx); err == nil {
		t.Fatal("开启保护但缺少导出函数应报错")
	}
	if has, _ := s.Has(ctx, orphan); !has {
		t.Fatal("备份失败时不得清扫任何对象")
	}
}
