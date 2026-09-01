package repo

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
	"github.com/jackc/pgx/v5"
)

// remoteDB 是 pull 测试使用的第二个数据库名。
const remoteDB = "caskb_remote_m3"

var dbCounter int64

// freshDB 为单个测试创建独立数据库,返回其 DSN,测试结束自动删除。
func freshDB(t *testing.T) string {
	dsn := testDSN(t)
	n := atomic.AddInt64(&dbCounter, 1)
	dbname := fmt.Sprintf("caskb_test_%d", n)
	ensureDB(t, siblingDSN(dsn, "postgres"), dbname)
	t.Cleanup(func() { dropDB(t, dbname) })
	return siblingDSN(dsn, dbname)
}

// siblingDSN 把 DSN 中的库名替换为目标库名。
func siblingDSN(dsn, db string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + db
	return u.String()
}

// ensureDB 重建指定数据库。
func ensureDB(t *testing.T, adminDSN, db string) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("连接 admin: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+db); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
}

// dropDB 删除测试数据库。
func dropDB(t *testing.T, db string) {
	ctx := context.Background()
	admin := siblingDSN(testDSN(t), "postgres")
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return
	}
	defer conn.Close(ctx)
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
}

// openRemote 打开一个独立的远端库(新库)。
func openRemote(t *testing.T) *store.PG {
	dsn := testDSN(t)
	ensureDB(t, siblingDSN(dsn, "postgres"), remoteDB)
	ctx := context.Background()
	s, err := store.Open(ctx, siblingDSN(dsn, remoteDB))
	if err != nil {
		t.Fatalf("打开远端: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testDSN 返回测试 DSN 或跳过。
func testDSN(t *testing.T) string {
	dsn := os.Getenv("KB_TEST_DSN")
	if dsn == "" {
		t.Skip("KB_TEST_DSN 未设置,跳过集成测试")
	}
	return dsn
}

// execDB 在指定测试库上执行一条 SQL(篡改/删除对象用)。
func execDB(t *testing.T, dsn, query string, args ...any) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
