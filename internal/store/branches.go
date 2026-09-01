package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/jackc/pgx/v5"
)

// BranchGet 读取分支指针;分支不存在返回 ErrBranchNotFound。
func (p *PG) BranchGet(ctx context.Context, name string) (hash.Address, error) {
	var addr string
	err := p.pool.QueryRow(ctx, "SELECT addr FROM branches WHERE name = $1", name).Scan(&addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrBranchNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: BranchGet 失败: %w", err)
	}
	return hash.Address(addr), nil
}

// BranchSet 推进分支指针(UPSERT)。目标对象不存在时由外键约束报错。
func (p *PG) BranchSet(ctx context.Context, name string, addr hash.Address) error {
	_, err := p.pool.Exec(ctx,
		"INSERT INTO branches (name, addr, updated_at) VALUES ($1,$2,now()) "+
			"ON CONFLICT (name) DO UPDATE SET addr = EXCLUDED.addr, updated_at = now()",
		name, string(addr))
	if err != nil {
		return fmt.Errorf("store: BranchSet 失败(目标对象需已存在): %w", err)
	}
	return nil
}

// BranchDelete 删除分支。不存在的分支视为成功。
func (p *PG) BranchDelete(ctx context.Context, name string) error {
	if _, err := p.pool.Exec(ctx, "DELETE FROM branches WHERE name = $1", name); err != nil {
		return fmt.Errorf("store: BranchDelete 失败: %w", err)
	}
	return nil
}

// BranchList 列出全部分支快照。
func (p *PG) BranchList(ctx context.Context) ([]BranchRef, error) {
	rows, err := p.pool.Query(ctx, "SELECT name, addr FROM branches ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("store: BranchList 失败: %w", err)
	}
	defer rows.Close()
	refs := []BranchRef{}
	for rows.Next() {
		var r BranchRef
		if err := rows.Scan(&r.Name, &r.Addr); err != nil {
			return nil, fmt.Errorf("store: BranchList 扫描失败: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: BranchList 迭代失败: %w", err)
	}
	return refs, nil
}
