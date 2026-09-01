package store

import (
	"context"
	"fmt"

	caskb "github.com/imeepos/cas-kb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBSchemaVersion 是当前实现支持的库 schema 版本,与 schema.sql 头部一致。
// 注意:与 object.SchemaVersion(对象编码格式版本)相互独立。
// 本实现不做存量库自动迁移:版本不符时在执行任何 DDL 之前拒绝打开,
// 老数据可弃则清库重建。
const DBSchemaVersion = 4

// Migrate 校验已有库版本(不符即在 DDL 前响亮拒绝),再对全新库执行 schema.sql。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := rejectIncompatibleVersion(ctx, pool); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, caskb.SchemaSQL); err != nil {
		return fmt.Errorf("store: 迁移 DDL 失败: %w", err)
	}
	var v string
	err := pool.QueryRow(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err != nil {
		return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
	}
	if v != fmt.Sprint(DBSchemaVersion) {
		return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开;"+
			"老数据可弃时请清空该库后重新执行 kb init", v, DBSchemaVersion)
	}
	return nil
}

// rejectIncompatibleVersion 对已存在且版本不符的库在 DDL 之前拒绝。
// 无 meta 表(全新库)或无版本行时放行,交由 schema.sql 初始化。
func rejectIncompatibleVersion(ctx context.Context, pool *pgxpool.Pool) error {
	var n int
	err := pool.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'meta'").Scan(&n)
	if err != nil {
		return fmt.Errorf("store: 检查 meta 表失败: %w", err)
	}
	if n == 0 {
		return nil
	}
	var v string
	err = pool.QueryRow(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err != nil {
		return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
	}
	if v != fmt.Sprint(DBSchemaVersion) {
		return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开;"+
			"老数据可弃时请清空该库后重新执行 kb init", v, DBSchemaVersion)
	}
	return nil
}
