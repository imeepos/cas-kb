package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// openSQLiteTest 打开一个独立 SQLite 测试库(零外部依赖,go test 默认即跑)。
func openSQLiteTest(t *testing.T) *SQLite {
	ctx := context.Background()
	s, err := openSQLite(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLite_OpenCreatesNestedDir(t *testing.T) {
	ctx := context.Background()
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "a", "b", "caskb.db")
	s, err := openSQLite(ctx, dsn)
	if err != nil {
		t.Fatalf("嵌套目录应自动创建: %v", err)
	}
	defer s.Close()
	if _, err := s.ProjectStats(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLite_OpenMemoryAndPrefixes(t *testing.T) {
	ctx := context.Background()
	for _, dsn := range []string{":memory:", "sqlite::memory:", "sqlite:" + filepath.Join(t.TempDir(), "x.db")} {
		s, err := openSQLite(ctx, dsn)
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", dsn, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLite_VersionGateRefuses(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTest(t)
	// 直接篡改版本行模拟旧库
	if _, err := s.db.ExecContext(ctx, "UPDATE meta SET value = '3' WHERE key = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// 重新打开必须拒绝(在 DDL 之前)
	s2, err := openSQLite(ctx, s.path)
	if err == nil {
		s2.Close()
		t.Fatal("版本不符的库应拒绝打开")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("错误应指向版本门禁: %v", err)
	}
}

func TestSQLite_PutGetRoundtripAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTest(t)
	data := []byte("sqlite roundtrip 内容")
	a1, err := s.Put(ctx, object.KindBlob, data)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.Put(ctx, object.KindBlob, data)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 || a1 != hash.Sum(data) {
		t.Fatalf("Put 应幂等且地址即哈希: %s %s", a1, a2)
	}
	got, kind, err := s.Get(ctx, a1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) || kind != object.KindBlob {
		t.Fatalf("roundtrip 不一致: %q %s", got, kind)
	}
	ok, err := s.Has(ctx, a1)
	if err != nil || !ok {
		t.Fatalf("Has 应为真: %v %v", ok, err)
	}
	if _, _, err := s.Get(ctx, hash.Address("sha256:"+strings.Repeat("f", 64))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缺失对象应返回 ErrNotFound: %v", err)
	}
}

func TestSQLite_DeleteAndList(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTest(t)
	a, err := s.Put(ctx, object.KindBlob, []byte("to be deleted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, a); err != nil { // 不存在视为成功
		t.Fatal(err)
	}
	if ok, _ := s.Has(ctx, a); ok {
		t.Fatal("删除后 Has 应为假")
	}
	n := 0
	if err := s.List(ctx, func(ObjectInfo) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("删除后 List 应为空,got %d", n)
	}
}

func TestSQLite_ProjectsAndBranches(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTest(t)

	// default 项目由 schema 播种
	stats, err := s.ProjectStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Project != "default" {
		t.Fatalf("全新库应仅有 default 项目: %+v", stats)
	}

	if err := s.ProjectCreate(ctx, "lab", "实验项目"); err != nil {
		t.Fatal(err)
	}
	// 幂等:重复创建不覆盖描述
	if err := s.ProjectCreate(ctx, "lab", "另一个描述"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectDescribe(ctx, "lab", "实验项目v2"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectDescribe(ctx, "ghost", "x"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("描述不存在的项目应报 ErrProjectNotFound: %v", err)
	}

	// 分支:目标对象不存在 → FK 拒绝
	ghost := hash.Address("sha256:" + strings.Repeat("a", 64))
	if err := s.BranchSet(ctx, "lab", "main", ghost); err == nil {
		t.Fatal("指向不存在对象的分支推进应被 FK 拒绝")
	}
	obj, err := s.Put(ctx, object.KindBlob, []byte("head blob"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "lab", "main", obj); err != nil {
		t.Fatal(err)
	}
	// 项目不存在 → FK 拒绝
	if err := s.BranchSet(ctx, "ghost", "main", obj); err == nil {
		t.Fatal("不存在项目的分支推进应被 FK 拒绝")
	}
	// 推进不覆盖描述
	if err := s.BranchDescribe(ctx, "lab", "main", "工作线"); err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "lab", "main", obj); err != nil {
		t.Fatal(err)
	}
	refs, err := s.BranchList(ctx, "lab")
	if err != nil || len(refs) != 1 {
		t.Fatalf("BranchList 应有一行: %+v %v", refs, err)
	}
	if refs[0].Description != "工作线" {
		t.Fatalf("分支推进不得清空描述: %+v", refs[0])
	}
	if err := s.BranchDescribe(ctx, "lab", "nope", "x"); !errors.Is(err, ErrBranchNotFound) {
		t.Fatalf("描述不存在的分支应报 ErrBranchNotFound: %v", err)
	}

	// 项目统计含分支数
	stats, err = s.ProjectStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ProjectStat{}
	for _, st := range stats {
		byName[st.Project] = st
	}
	if byName["lab"].Branches != 1 || byName["default"].Branches != 0 {
		t.Fatalf("分支统计不符: %+v", stats)
	}
	if err := s.BranchDelete(ctx, "lab", "main"); err != nil {
		t.Fatal(err)
	}
	all, err := s.BranchListAll(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("删除后 BranchListAll 应为空: %+v %v", all, err)
	}
}

func TestSQLite_ListNestedCallback(t *testing.T) {
	// GC/FSCK 形态:List 游标遍历中嵌套 Get/Delete,不得死锁
	ctx := context.Background()
	s := openSQLiteTest(t)
	var addrs []hash.Address
	for i := 0; i < 8; i++ {
		a, err := s.Put(ctx, object.KindBlob, []byte{byte('a' + i)})
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, a)
	}
	swept := 0
	err := s.List(ctx, func(info ObjectInfo) error {
		data, kind, err := s.Get(ctx, info.Addr)
		if err != nil {
			return err
		}
		_ = kind
		if string(data) == "a" { // 首字母保留,其余删
			return nil
		}
		if err := s.Delete(ctx, info.Addr); err != nil {
			return err
		}
		swept++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if swept != 7 {
		t.Fatalf("应清扫 7 个对象,got %d", swept)
	}
}

func TestSQLite_WipeResetsToFresh(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTest(t)
	if _, err := s.Put(ctx, object.KindBlob, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectCreate(ctx, "lab", ""); err != nil {
		t.Fatal(err)
	}
	obj, _ := s.Put(ctx, object.KindBlob, []byte("head"))
	if err := s.BranchSet(ctx, "lab", "main", obj); err != nil {
		t.Fatal(err)
	}
	if err := s.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	// 等价全新库:对象清零,仅剩 default 项目,可继续写入
	n := 0
	if err := s.List(ctx, func(ObjectInfo) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("wipe 后对象应为零,got %d", n)
	}
	stats, err := s.ProjectStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Project != "default" {
		t.Fatalf("wipe 后应仅剩 default 项目: %+v", stats)
	}
	a, err := s.Put(ctx, object.KindBlob, []byte("after wipe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "default", "main", a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BranchGet(ctx, "default", "main"); err != nil {
		t.Fatal(err)
	}
}
