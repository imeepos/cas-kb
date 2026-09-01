// Package testdb 为集成测试提供统一的测试库机制:
// 以 KB_TEST_DSN 为基库,为每个用例派生一个全新的独立数据库,
// 用例结束自动删除;未设置 KB_TEST_DSN 时跳过集成测试。
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

var counter int64

// DSN 返回基库连接串(KB_TEST_DSN);未设置时跳过当前测试。
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KB_TEST_DSN")
	if dsn == "" {
		t.Skip("KB_TEST_DSN 未设置,跳过集成测试")
	}
	return dsn
}

// NewSQLite 派生一个全新的 SQLite 测试库文件(t.TempDir 内,结束自动清理)。
// SQLite 用例零外部依赖,go test 默认即跑。
func NewSQLite(t *testing.T) string {
	t.Helper()
	n := atomic.AddInt64(&counter, 1)
	return "sqlite:" + filepath.Join(t.TempDir(), fmt.Sprintf("caskb_test_%d.db", n))
}

// IsSQLite 报告 DSN 是否为 SQLite 形态(非 postgres:// 即 SQLite)。
func IsSQLite(dsn string) bool {
	return !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://")
}

// New 派生一个全新的测试数据库,返回其 DSN,测试结束自动删除。
func New(t *testing.T) string {
	t.Helper()
	base := DSN(t)
	n := atomic.AddInt64(&counter, 1)
	name := fmt.Sprintf("caskb_test_%d_%d", os.Getpid(), n)
	Rebuild(t, Sibling(base, "postgres"), name)
	t.Cleanup(func() { Drop(t, name) })
	return Sibling(base, name)
}

// Sibling 把 DSN 中的库名替换为目标库名。
func Sibling(dsn, db string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("testdb: 解析 DSN: %v", err))
	}
	u.Path = "/" + db
	return u.String()
}

// Rebuild 重建指定数据库(先删后建)。
func Rebuild(t *testing.T, adminDSN, db string) {
	t.Helper()
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

// Drop 删除测试数据库(尽力而为,失败不阻塞用例收尾)。
func Drop(t *testing.T, db string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, Sibling(DSN(t), "postgres"))
	if err != nil {
		return
	}
	defer conn.Close(ctx)
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
}

// Exec 在指定库上执行一条 SQL(篡改/删除对象用)。
func Exec(t *testing.T, dsn, query string, args ...any) {
	t.Helper()
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
