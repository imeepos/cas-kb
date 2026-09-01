package store

import (
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// v1DDL 按 schema v1 形状建表,模拟未经迁移的存量旧库。
var v1DDL = strings.Join([]string{
	"CREATE TABLE objects (",
	"    addr text PRIMARY KEY,",
	"    kind text NOT NULL,",
	"    size integer NOT NULL,",
	"    data bytea NOT NULL",
	");",
	"CREATE TABLE branches (",
	"    name text PRIMARY KEY,",
	"    addr text NOT NULL REFERENCES objects(addr),",
	"    updated_at timestamptz NOT NULL DEFAULT now()",
	");",
	"CREATE TABLE meta (",
	"    key text PRIMARY KEY,",
	"    value text NOT NULL",
	");",
	"INSERT INTO meta VALUES ('schema_version', '1');",
}, "\n")

func TestMigrateRejectsV1Database(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, v1DDL); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, dsn)
	if err == nil {
		t.Fatal("v1 旧库应被拒绝打开(不做自动迁移)")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("错误应指向 schema_version 不匹配: %v", err)
	}
	if !strings.Contains(err.Error(), "重新执行 kb init") {
		t.Fatalf("错误应给出清库重建指引: %v", err)
	}
}
