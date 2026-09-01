package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

// projectOverride 由全局参数 -p 设置,优先于 KB_PROJECT。
var projectOverride string

// projectName 返回项目作用域(-p > KB_PROJECT > default)。
func projectName() string {
	if projectOverride != "" {
		return projectOverride
	}
	if p := os.Getenv("KB_PROJECT"); p != "" {
		return p
	}
	return "default"
}

// branchName 返回默认分支名(KB_BRANCH,默认 main)。
func branchName() string {
	if b := os.Getenv("KB_BRANCH"); b != "" {
		return b
	}
	return "main"
}

// defaultSQLitePath 返回默认 SQLite 库文件路径:
// XDG_DATA_HOME(或 ~/.local/share)/caskb/caskb.db。
func defaultSQLitePath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "caskb", "caskb.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "caskb.db" // 无家目录的极端环境:退到工作目录
	}
	return filepath.Join(home, ".local", "share", "caskb", "caskb.db")
}

// effectiveDSN 返回生效连接串:KB_DSN > sqlite 默认路径。
// KB_DSN 指向 postgres:// 时即使用 PostgreSQL 后端,其余形态按 SQLite 路径处理。
func effectiveDSN() string {
	if dsn := os.Getenv("KB_DSN"); dsn != "" {
		return dsn
	}
	return "sqlite:" + defaultSQLitePath()
}

// openStore 打开(含迁移)本地存储,后端由 DSN 分派(默认 SQLite)。
func openStore(ctx context.Context) (store.Store, error) {
	return store.Open(ctx, effectiveDSN())
}

// openRepo 打开本地存储并构造仓库对象,调用方负责 Close。
func openRepo(ctx context.Context) (*repo.Repo, store.Store, error) {
	s, err := openStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	cfg := repo.Config{Project: projectName(), Branch: branchName()}
	applyGCProtection(&cfg)
	r := repo.Open(s, cfg)
	return r, s, nil
}
