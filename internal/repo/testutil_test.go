package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// freshDB 为单个测试创建独立数据库,返回其 DSN,测试结束自动删除。
func freshDB(t *testing.T) string { return testdb.New(t) }

// openRemote 打开一个独立的远端库(每个用例独立新库)。
func openRemote(t *testing.T) *store.PG {
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatalf("打开远端: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// execDB 在指定测试库上执行一条 SQL(篡改/删除对象用)。
func execDB(t *testing.T, dsn, query string, args ...any) {
	testdb.Exec(t, dsn, query, args...)
}
