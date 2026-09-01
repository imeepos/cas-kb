package main

import (
	"context"
	"os"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

const defaultDSN = "postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable"

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

// openStore 打开(含迁移)本地存储。
func openStore(ctx context.Context) (*store.PG, error) {
	dsn := os.Getenv("KB_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}
	return store.Open(ctx, dsn)
}

// openRepo 打开本地存储并构造仓库对象,调用方负责 Close。
func openRepo(ctx context.Context) (*repo.Repo, *store.PG, error) {
	s, err := openStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	cfg := repo.Config{Project: projectName(), Branch: branchName()}
	applyGCProtection(&cfg)
	r := repo.Open(s, cfg)
	return r, s, nil
}
