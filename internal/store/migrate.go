package store

import (
	"context"
	"fmt"

	caskb "github.com/imeepos/cas-kb"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpectedSchemaVersion 是当前实现支持的库 schema 版本,与 object.SchemaVersion 一致。
const ExpectedSchemaVersion = object.SchemaVersion

// Migrate 执行 DDL(schema.sql,幂等)并把 meta.schema_version 推进到期望值。
// 若库中已有更高的不兼容版本,返回错误(误配置响亮失败)。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, caskb.SchemaSQL); err != nil {
		return fmt.Errorf("store: 迁移 DDL 失败: %w", err)
	}
	var v string
	err := pool.QueryRow(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err != nil {
		return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
	}
	if v != fmt.Sprint(ExpectedSchemaVersion) {
		return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开", v, ExpectedSchemaVersion)
	}
	return nil
}