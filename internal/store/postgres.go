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
