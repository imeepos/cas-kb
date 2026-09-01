package store

import (
	"context"
	"fmt"

	caskb "github.com/imeepos/cas-kb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBSchemaVersion 是当前实现支持的库 schema 版本,与 schema.sql 头部一致。
// 注意:与 object.SchemaVersion(对象编码格式版本)相互独立——
// v2 仅变更表结构(projects 表 + branches 项目维度),对象编码与地址不变。
const DBSchemaVersion = 2

// Migrate 分四步把任意版本的库带到位(全部幂等):
//  1. v1 结构列迁移(补 project 列/复合主键,回填 default,推进版本号)
//  2. 执行 schema.sql(目标形态 DDL,IF NOT EXISTS)
//  3. 补齐 project 外键(依赖 projects 表,故在 DDL 之后)
//  4. 校验 meta.schema_version,不匹配响亮失败
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := migrateV1Branches(ctx, pool); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, caskb.SchemaSQL); err != nil {
		return fmt.Errorf("store: 迁移 DDL 失败: %w", err)
	}
	if err := ensureBranchesProjectFK(ctx, pool); err != nil {
		return err
	}
	var v string
	err := pool.QueryRow(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err != nil {
		return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
	}
	if v != fmt.Sprint(DBSchemaVersion) {
		return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开", v, DBSchemaVersion)
	}
	return nil
}

// migrateV1Branches 给 v1 形状的 branches 表补 project 维度并回填 default。
// 表不存在(全新库)或已有 project 列(v2)时为空操作;此步不含外键。
func migrateV1Branches(ctx context.Context, pool *pgxpool.Pool) error {
	exists, err := count(ctx, pool,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'branches'")
	if err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	hasProject, err := count(ctx, pool,
		"SELECT count(*) FROM information_schema.columns "+
			"WHERE table_name = 'branches' AND column_name = 'project'")
	if err != nil {
		return err
	}
	if hasProject > 0 {
		return nil
	}
	for _, q := range []string{
		"ALTER TABLE branches ADD COLUMN project text NOT NULL DEFAULT 'default'",
		"ALTER TABLE branches DROP CONSTRAINT IF EXISTS branches_pkey",
		"ALTER TABLE branches ADD PRIMARY KEY (project, name)",
		"UPDATE meta SET value = '2' WHERE key = 'schema_version'",
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("store: v1→v2 迁移执行失败(%s): %w", q, err)
		}
	}
	return nil
}

// ensureBranchesProjectFK 在 projects 表就绪后补齐 project 外键(缺失才加)。
func ensureBranchesProjectFK(ctx context.Context, pool *pgxpool.Pool) error {
	n, err := count(ctx, pool,
		"SELECT count(*) FROM information_schema.table_constraints "+
			"WHERE table_name = 'branches' AND constraint_name = 'branches_project_fkey'")
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	const q = "ALTER TABLE branches ADD CONSTRAINT branches_project_fkey " +
		"FOREIGN KEY (project) REFERENCES projects(name)"
	if _, err := pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("store: 补 project 外键失败: %w", err)
	}
	return nil
}

// count 执行返回单个整数的查询。
func count(ctx context.Context, pool *pgxpool.Pool, query string) (int, error) {
	var n int
	if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: 检查迁移状态失败: %w", err)
	}
	return n, nil
}
