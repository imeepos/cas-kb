package store

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// M3.8:按指引「清空该库后重新执行 kb init」的 TRUNCATE 路径——
// 表在、数据空(meta 无版本行)应等价全新库,不得被版本门禁拒绝。
func TestMigrateAcceptsTruncatedDatabase(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	if _, err := Open(ctx, dsn); err != nil {
		t.Fatalf("首次 Open 应建立 v4 结构: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "TRUNCATE branches, objects, projects, meta"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dsn); err != nil {
		t.Fatalf("清空后的库应等价全新库: %v", err)
	}
}