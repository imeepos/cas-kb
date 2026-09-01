package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/jackc/pgx/v5"
)

// BranchGet 读取分支指针;分支不存在返回 ErrBranchNotFound。
func (p *PG) BranchGet(ctx context.Context, project, name string) (hash.Address, error) {
	var addr string
	err := p.pool.QueryRow(ctx,
		"SELECT addr FROM branches WHERE project = $1 AND name = $2",
		project, name).Scan(&addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrBranchNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: BranchGet 失败: %w", err)
	}
	return hash.Address(addr), nil
}

// BranchSet 推进分支指针(UPSERT)。目标对象不存在时由外键约束报错。
func (p *PG) BranchSet(ctx context.Context, project, name string, addr hash.Address) error {
	_, err := p.pool.Exec(ctx,
		"INSERT INTO branches (project, name, addr, updated_at) VALUES ($1,$2,$3,now()) "+
			"ON CONFLICT (project, name) DO UPDATE SET addr = EXCLUDED.addr, updated_at = now()",
		project, name, string(addr))
	if err != nil {
		return fmt.Errorf("store: BranchSet 失败(目标对象需已存在): %w", err)
	}
	return nil
}

// BranchDelete 删除分支。不存在的分支视为成功。
func (p *PG) BranchDelete(ctx context.Context, project, name string) error {
	if _, err := p.pool.Exec(ctx,
		"DELETE FROM branches WHERE project = $1 AND name = $2",
		project, name); err != nil {
		return fmt.Errorf("store: BranchDelete 失败: %w", err)
	}
	return nil
}

// BranchList 列出单个项目内的全部分支快照。
func (p *PG) BranchList(ctx context.Context, project string) ([]BranchRef, error) {
	rows, err := p.pool.Query(ctx,
		"SELECT name, addr FROM branches WHERE project = $1 ORDER BY name", project)
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
	return refs, rows.Err()
}

// BranchListAll 列出所有项目的全部分支,仅供 GC 标记与分支表备份使用。
func (p *PG) BranchListAll(ctx context.Context) ([]BranchRef, error) {
	rows, err := p.pool.Query(ctx,
		"SELECT project, name, addr FROM branches ORDER BY project, name")
	if err != nil {
		return nil, fmt.Errorf("store: BranchListAll 失败: %w", err)
	}
	defer rows.Close()
	refs := []BranchRef{}
	for rows.Next() {
		var r BranchRef
		if err := rows.Scan(&r.Project, &r.Name, &r.Addr); err != nil {
			return nil, fmt.Errorf("store: BranchListAll 扫描失败: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
