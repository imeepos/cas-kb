package store

import (
	"context"
	"fmt"
)

// ProjectStat 是项目及其分支数的摘要。
type ProjectStat struct {
	Project  string
	Branches int
}

// ProjectCreate 创建项目;已存在则等价空操作(幂等)。
func (p *PG) ProjectCreate(ctx context.Context, name string) error {
	if _, err := p.pool.Exec(ctx,
		"INSERT INTO projects (name) VALUES ($1) ON CONFLICT (name) DO NOTHING", name); err != nil {
		return fmt.Errorf("store: ProjectCreate 失败: %w", err)
	}
	return nil
}

// ProjectStats 列出全部项目及各自的分支数(字典序)。
func (p *PG) ProjectStats(ctx context.Context) ([]ProjectStat, error) {
	rows, err := p.pool.Query(ctx,
		"SELECT p.name, count(b.name) FROM projects p "+
			"LEFT JOIN branches b ON b.project = p.name "+
			"GROUP BY p.name ORDER BY p.name")
	if err != nil {
		return nil, fmt.Errorf("store: ProjectStats 失败: %w", err)
	}
	defer rows.Close()
	stats := []ProjectStat{}
	for rows.Next() {
		var s ProjectStat
		if err := rows.Scan(&s.Project, &s.Branches); err != nil {
			return nil, fmt.Errorf("store: ProjectStats 扫描失败: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
