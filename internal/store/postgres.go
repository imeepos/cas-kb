package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PG 是 postgres 后端实现,持有连接池。
type PG struct {
	pool *pgxpool.Pool
}

// Open 连接 DSN,执行迁移与 schema 校验,返回可用的存储实现。
func Open(ctx context.Context, dsn string) (*PG, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: 连接失败: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping 失败: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &PG{pool: pool}, nil
}

// Close 释放连接池。
func (p *PG) Close() error {
	p.pool.Close()
	return nil
}

// Wipe 清空全部业务数据(TRUNCATE 四表,单语句满足外键约束),
// 再重跑 schema.sql 播种默认项目与 schema_version,结果等价全新初始化的库。
func (p *PG) Wipe(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, "TRUNCATE branches, objects, projects, meta"); err != nil {
		return fmt.Errorf("store: Wipe 清空失败: %w", err)
	}
	if err := Migrate(ctx, p.pool); err != nil {
		return fmt.Errorf("store: Wipe 后重新播种失败: %w", err)
	}
	return nil
}
