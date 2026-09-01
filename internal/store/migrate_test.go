package store

import (
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// v1DDL 按 schema v1 形状建表,模拟未经迁移的存量库。
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

func TestMigrateUpgradesV1ToV2PreservingBranches(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	legacy := hash.Address("sha256:" + strings.Repeat("ab", 32))

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, v1DDL); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx,
		"INSERT INTO objects(addr, kind, size, data) VALUES ($1, 'blob', 2, $2)",
		string(legacy), []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx,
		"INSERT INTO branches(name, addr) VALUES ('legacy-main', $1)", string(legacy)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dsn) // 触发 v1→v2 自动迁移
	if err != nil {
		t.Fatalf("v1 库应自动迁移并成功打开: %v", err)
	}
	defer s.Close()
	got, err := s.BranchGet(ctx, "legacy-main")
	if err != nil {
		t.Fatalf("迁移后原分支应可读: %v", err)
	}
	if got != legacy {
		t.Fatalf("迁移后分支地址应不变: %s != %s", got, legacy)
	}

	check, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close(ctx)
	var proj, ver string
	if err := check.QueryRow(ctx,
		"SELECT project FROM branches WHERE name = 'legacy-main'").Scan(&proj); err != nil {
		t.Fatal(err)
	}
	if proj != "default" {
		t.Fatalf("存量分支应落入 default 项目,got %q", proj)
	}
	if err := check.QueryRow(ctx,
		"SELECT value FROM meta WHERE key = 'schema_version'").Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != "2" {
		t.Fatalf("迁移后库版本应为 2,got %q", ver)
	}
}
