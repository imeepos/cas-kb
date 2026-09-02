package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// meta 表是库级 KV(config/M3.9 已有 DDL):这里补充通用读写契约,
// 供 gc 保留水位等特性使用。schema_version 等系统键不经过本接口。

// MetaGet 读取 meta 表键值;键不存在返回 ErrNotFound。(PostgreSQL)
func (p *PG) MetaGet(ctx context.Context, key string) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, "SELECT value FROM meta WHERE key = $1", key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: MetaGet 失败: %w", err)
	}
	return v, nil
}

// MetaSet 写入 meta 表键值(UPSERT)。(PostgreSQL)
func (p *PG) MetaSet(ctx context.Context, key, value string) error {
	if _, err := p.pool.Exec(ctx,
		"INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value); err != nil {
		return fmt.Errorf("store: MetaSet 失败: %w", err)
	}
	return nil
}

// MetaDelete 删除 meta 表键;键不存在等价空操作(幂等)。(PostgreSQL)
func (p *PG) MetaDelete(ctx context.Context, key string) error {
	if _, err := p.pool.Exec(ctx, "DELETE FROM meta WHERE key = $1", key); err != nil {
		return fmt.Errorf("store: MetaDelete 失败: %w", err)
	}
	return nil
}

// MetaGet 读取 meta 表键值;键不存在返回 ErrNotFound。(SQLite)
func (s *SQLite) MetaGet(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: MetaGet 失败: %w", err)
	}
	return v, nil
}

// MetaSet 写入 meta 表键值(UPSERT)。(SQLite)
func (s *SQLite) MetaSet(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value); err != nil {
		return fmt.Errorf("store: MetaSet 失败: %w", err)
	}
	return nil
}

// MetaDelete 删除 meta 表键;键不存在等价空操作(幂等)。(SQLite)
func (s *SQLite) MetaDelete(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM meta WHERE key = ?", key); err != nil {
		return fmt.Errorf("store: MetaDelete 失败: %w", err)
	}
	return nil
}
