package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
)

// ProjectCreate 创建项目(可带描述);已存在则等价空操作(幂等,不覆盖既有描述)。
func (s *SQLite) ProjectCreate(ctx context.Context, name, description string) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO projects (name, description) VALUES (?, ?) ON CONFLICT (name) DO NOTHING",
		name, description); err != nil {
		return fmt.Errorf("store: ProjectCreate 失败: %w", err)
	}
	return nil
}

// ProjectGet 读取单个项目的名称/描述/分支数;不存在返回 ErrProjectNotFound。
func (s *SQLite) ProjectGet(ctx context.Context, name string) (ProjectStat, error) {
	var stat ProjectStat
	err := s.db.QueryRowContext(ctx,
		"SELECT p.name, p.description, count(b.name) FROM projects p "+
			"LEFT JOIN branches b ON b.project = p.name "+
			"WHERE p.name = ? GROUP BY p.name, p.description", name).
		Scan(&stat.Project, &stat.Description, &stat.Branches)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectStat{}, ErrProjectNotFound
	}
	if err != nil {
		return ProjectStat{}, fmt.Errorf("store: ProjectGet 失败: %w", err)
	}
	return stat, nil
}

// ProjectDescribe 就地更新项目描述(命名空间元数据,不产生快照)。
// 项目不存在返回 ErrProjectNotFound。
func (s *SQLite) ProjectDescribe(ctx context.Context, name, description string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE projects SET description = ? WHERE name = ?", description, name)
	if err != nil {
		return fmt.Errorf("store: ProjectDescribe 失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// ProjectStats 列出全部项目及各自的描述与分支数(字典序)。
func (s *SQLite) ProjectStats(ctx context.Context) ([]ProjectStat, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT p.name, p.description, count(b.name) FROM projects p "+
			"LEFT JOIN branches b ON b.project = p.name "+
			"GROUP BY p.name, p.description ORDER BY p.name")
	if err != nil {
		return nil, fmt.Errorf("store: ProjectStats 失败: %w", err)
	}
	defer rows.Close()
	stats := []ProjectStat{}
	for rows.Next() {
		var stat ProjectStat
		if err := rows.Scan(&stat.Project, &stat.Description, &stat.Branches); err != nil {
			return nil, fmt.Errorf("store: ProjectStats 扫描失败: %w", err)
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

// BranchGet 读取分支指针;分支不存在返回 ErrBranchNotFound。
func (s *SQLite) BranchGet(ctx context.Context, project, name string) (hash.Address, error) {
	var addr string
	err := s.db.QueryRowContext(ctx,
		"SELECT addr FROM branches WHERE project = ? AND name = ?",
		project, name).Scan(&addr)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrBranchNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: BranchGet 失败: %w", err)
	}
	return hash.Address(addr), nil
}

// BranchSet 推进分支指针(UPSERT)。目标对象不存在时由外键约束报错。
// UPDATE 集仅含 addr/updated_at:推进不清空既有描述(命名空间元数据)。
func (s *SQLite) BranchSet(ctx context.Context, project, name string, addr hash.Address) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO branches (project, name, addr, updated_at) VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now')) "+
			"ON CONFLICT (project, name) DO UPDATE SET addr = excluded.addr, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')",
		project, name, string(addr))
	if err != nil {
		return fmt.Errorf("store: BranchSet 失败(目标对象需已存在): %w", err)
	}
	return nil
}

// BranchDescribe 就地更新分支描述(命名空间元数据,不产生快照)。
// 分支不存在返回 ErrBranchNotFound。
func (s *SQLite) BranchDescribe(ctx context.Context, project, name, description string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE branches SET description = ? WHERE project = ? AND name = ?", description, project, name)
	if err != nil {
		return fmt.Errorf("store: BranchDescribe 失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrBranchNotFound
	}
	return nil
}

// BranchDelete 删除分支。不存在的分支视为成功。
func (s *SQLite) BranchDelete(ctx context.Context, project, name string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM branches WHERE project = ? AND name = ?",
		project, name); err != nil {
		return fmt.Errorf("store: BranchDelete 失败: %w", err)
	}
	return nil
}

// BranchList 列出单个项目内的全部分支快照(含描述)。
func (s *SQLite) BranchList(ctx context.Context, project string) ([]BranchRef, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name, addr, description FROM branches WHERE project = ? ORDER BY name", project)
	if err != nil {
		return nil, fmt.Errorf("store: BranchList 失败: %w", err)
	}
	defer rows.Close()
	refs := []BranchRef{}
	for rows.Next() {
		var r BranchRef
		if err := rows.Scan(&r.Name, &r.Addr, &r.Description); err != nil {
			return nil, fmt.Errorf("store: BranchList 扫描失败: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// BranchListAll 列出所有项目的全部分支(含描述),仅供 GC 标记与分支表备份使用。
func (s *SQLite) BranchListAll(ctx context.Context) ([]BranchRef, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT project, name, addr, description FROM branches ORDER BY project, name")
	if err != nil {
		return nil, fmt.Errorf("store: BranchListAll 失败: %w", err)
	}
	defer rows.Close()
	refs := []BranchRef{}
	for rows.Next() {
		var r BranchRef
		if err := rows.Scan(&r.Project, &r.Name, &r.Addr, &r.Description); err != nil {
			return nil, fmt.Errorf("store: BranchListAll 扫描失败: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
