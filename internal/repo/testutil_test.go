package repo

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// 测试后端选择:设置 KB_TEST_DSN 时跑 PostgreSQL 回归;
// 默认 SQLite(零外部依赖,go test 即跑)。两后端跑同一套用例。
func onSQLite() bool { return os.Getenv("KB_TEST_DSN") == "" }

// freshDB 为单个测试创建独立数据库,返回其 DSN,测试结束自动清理。
func freshDB(t *testing.T) string {
	t.Helper()
	if onSQLite() {
		return testdb.NewSQLite(t)
	}
	return testdb.New(t)
}

// openRemote 打开一个独立的远端库(每个用例独立新库)。
func openRemote(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.NewSQLite(t)
	if !onSQLite() {
		dsn = testdb.New(t)
	}
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开远端: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// execDB 在指定测试库上执行一条 SQL(篡改/删除对象用)。
// 语句按 PG 风格 $1..$N 书写;SQLite 时自动转 ? 并按编号重排参数。
func execDB(t *testing.T, dsn, query string, args ...any) {
	t.Helper()
	if !testdb.IsSQLite(dsn) {
		testdb.Exec(t, dsn, query, args...)
		return
	}
	re := regexp.MustCompile("\\$(\\d+)")
	order := make([]any, 0, len(args))
	for _, m := range re.FindAllStringSubmatch(query, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > len(args) {
			t.Fatalf("execDB: 非法占位符 $%s", m[1])
		}
		order = append(order, args[n-1])
	}
	q := re.ReplaceAllString(query, "?")
	db, err := sql.Open("sqlite", strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:"), "file:"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, order...); err != nil {
		t.Fatal(err)
	}
}
