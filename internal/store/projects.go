package store

import (
	"context"
	"fmt"
)

// ProjectCreate 创建项目;已存在则等价空操作(幂等)。
func (p *PG) ProjectCreate(ctx context.Context, name string) error {
	if _, err := p.pool.Exec(ctx,
		"INSERT INTO projects (name) VALUES ($1) ON CONFLICT (name) DO NOTHING", name); err != nil {
		return fmt.Errorf("store: ProjectCreate 失败: %w", err)
	}
	return nil
}

// ProjectList 列出全部项目名(字典序)。
func (p *PG) ProjectList(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, "SELECT name FROM projects ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("store: ProjectList 失败: %w", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("store: ProjectList 扫描失败: %w", err)
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
