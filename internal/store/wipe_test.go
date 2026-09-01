package store

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
)

// M3.9:Wipe 清空全部业务数据并重新播种,等价全新初始化的库。
func TestWipeResetsToFreshLibrary(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, err := s.Put(ctx, object.KindBlob, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectCreate(ctx, "alpha", "演示"); err != nil {
		t.Fatal(err)
	}
	snapAddr, err := s.Put(ctx, object.KindBlob, []byte("snap"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "alpha", "main", snapAddr); err != nil {
		t.Fatal(err)
	}

	if err := s.Wipe(ctx); err != nil {
		t.Fatalf("Wipe 应成功: %v", err)
	}

	count := 0
	if err := s.List(ctx, func(ObjectInfo) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("清空后对象数应为 0,得到 %d", count)
	}
	branches, err := s.BranchListAll(ctx)
	if err != nil || len(branches) != 0 {
		t.Fatalf("清空后分支应为 0: %v %v", branches, err)
	}
	projects, err := s.ProjectStats(ctx)
	if err != nil || len(projects) != 1 || projects[0].Project != "default" {
		t.Fatalf("清空后应只剩默认项目: %+v %v", projects, err)
	}
	// 清空后仍可正常写入(库可用)
	if _, err := s.Put(ctx, object.KindBlob, []byte("again")); err != nil {
		t.Fatalf("清空后应可写入: %v", err)
	}
}
