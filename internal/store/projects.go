package store

import (
	"context"
	"fmt"
)

// ProjectStat 是项目及其描述、分支数的摘要。
type ProjectStat struct {
	Project     string
	Description string
	Branches    int
}

// ProjectCreate 创建项目(可带描述);已存在则等价空操作(幂等,不覆盖既有描述)。
func (p *PG) ProjectCreate(ctx context.Context, name, description string) error {
	if _, err := p.pool.Exec(ctx,
		"INSERT INTO projects (name, description) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING", name, description); err != nil {
		return fmt.Errorf("store: ProjectCreate 失败: %w", err)
	}
	return nil
}

// ProjectDescribe 就地更新项目描述(命名空间元数据,不产生快照)。
// 项目不存在返回 ErrProjectNotFound。
func (p *PG) ProjectDescribe(ctx context.Context, name, description string) error {
	tag, err := p.pool.Exec(ctx,
		"UPDATE projects SET description = $2 WHERE name = $1", name, description)
	if err != nil {
		return fmt.Errorf("store: ProjectDescribe 失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// ProjectStats 列出全部项目及各自的描述与分支数(字典序)。
func (p *PG) ProjectStats(ctx context.Context) ([]ProjectStat, error) {
	rows, err := p.pool.Query(ctx,
		"SELECT p.name, p.description, count(b.name) FROM projects p "+
			"LEFT JOIN branches b ON b.project = p.name "+
			"GROUP BY p.name, p.description ORDER BY p.name")
	if err != nil {
		return nil, fmt.Errorf("store: ProjectStats 失败: %w", err)
	}
	defer rows.Close()
	stats := []ProjectStat{}
	for rows.Next() {
		var s ProjectStat
		if err := rows.Scan(&s.Project, &s.Description, &s.Branches); err != nil {
			return nil, fmt.Errorf("store: ProjectStats 扫描失败: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
