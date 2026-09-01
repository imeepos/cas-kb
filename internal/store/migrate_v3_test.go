package store

import (
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// v3DDL 按 schema v3 形状建表(含描述列但版本为 3),模拟 v4 实现面对的存量旧库。
var v3DDL = strings.Join([]string{
	"CREATE TABLE objects (",
	"    addr text PRIMARY KEY,",
	"    kind text NOT NULL,",
	"    size integer NOT NULL,",
	"    data bytea NOT NULL",
	");",
	"CREATE TABLE projects (",
	"    name text PRIMARY KEY,",
	"    created_at timestamptz NOT NULL DEFAULT now(),",
	"    description text NOT NULL DEFAULT ''",
	");",
	"CREATE TABLE branches (",
	"    project text NOT NULL DEFAULT 'default' REFERENCES projects(name),",
	"    name text NOT NULL,",
	"    addr text NOT NULL REFERENCES objects(addr),",
	"    updated_at timestamptz NOT NULL DEFAULT now(),",
	"    description text NOT NULL DEFAULT ''",
	");",
	"CREATE TABLE meta (",
	"    key text PRIMARY KEY,",
	"    value text NOT NULL",
	");",
	"INSERT INTO meta VALUES ('schema_version', '3');",
}, "\n")

// M3.8:v3 存量库打开时必须拒绝服务并给出重建指引(不做自动迁移)。
// v4 的 tree 编码带类型条目,v3 旧格式对象无法通过 v4 解码。
func TestMigrateRejectsV3Database(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, v3DDL); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, dsn)
	if err == nil {
		t.Fatal("v3 旧库应被拒绝打开(不做自动迁移)")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("错误应指向 schema_version 不匹配: %v", err)
	}
}
