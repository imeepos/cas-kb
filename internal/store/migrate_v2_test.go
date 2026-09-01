package store

import (
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// v2DDL 按 schema v2 形状建表(无 description 列),模拟 v3 实现面对的存量旧库。
var v2DDL = strings.Join([]string{
	"CREATE TABLE objects (",
	"    addr text PRIMARY KEY,",
	"    kind text NOT NULL,",
	"    size integer NOT NULL,",
	"    data bytea NOT NULL",
	");",
	"CREATE TABLE projects (",
	"    name text PRIMARY KEY,",
	"    created_at timestamptz NOT NULL DEFAULT now()",
	");",
	"CREATE TABLE branches (",
	"    project text NOT NULL DEFAULT 'default' REFERENCES projects(name),",
	"    name text NOT NULL,",
	"    addr text NOT NULL REFERENCES objects(addr),",
	"    updated_at timestamptz NOT NULL DEFAULT now(),",
	"    PRIMARY KEY (project, name)",
	");",
	"CREATE TABLE meta (",
	"    key text PRIMARY KEY,",
	"    value text NOT NULL",
	");",
	"INSERT INTO meta VALUES ('schema_version', '2');",
}, "\n")

// M3.7:v2 存量库打开时必须拒绝服务并给出重建指引(不做自动迁移)。
func TestMigrateRejectsV2Database(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, v2DDL); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, dsn)
	if err == nil {
		t.Fatal("v2 旧库应被拒绝打开(不做自动迁移)")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("错误应指向 schema_version 不匹配: %v", err)
	}
	if !strings.Contains(err.Error(), "重新执行 kb init") {
		t.Fatalf("错误应给出清库重建指引: %v", err)
	}
}
