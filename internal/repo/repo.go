// Package repo 实现业务层:条目读写、版本日志、diff、pull、gc、fsck。
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// Config 是仓库打开选项。
type Config struct {
	// Project 是项目作用域(M3.5 项目隔离),默认 "default"。
	Project string
	// Branch 是默认工作分支名,默认 "main"。
	Branch string
	// Now 注入时间源,便于测试;为空时用 time.Now。
	Now func() int64
	// GCProtect 开启后,GC 清扫前先导出分支表(误删保护)。
	GCProtect bool
	// GCExportBranches 接收分支表备份;GCProtect 为 true 时必须提供。
	GCExportBranches func(ctx context.Context, branches []store.BranchRef) error
	// NoIndex 为 true 时,该仓库视图的提交不构建检索索引(index 置空)。
	// 仅暂存分支视图使用;正式分支提交必须构建索引。
	NoIndex bool
}

// Repo 是一个已打开的知识库仓库。依赖 store,不反向依赖。
type Repo struct {
	st        store.Store
	project   string
	branch    string
	now       func() int64
	gcProtect bool
	gcExport  func(ctx context.Context, branches []store.BranchRef) error
	skipIndex bool
}

// Open 构造仓库。Store 由调用方打开并负责 Close。
func Open(s store.Store, cfg Config) *Repo {
	project := cfg.Project
	if project == "" {
		project = "default"
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	now := cfg.Now
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}
	return &Repo{st: s, project: project, branch: branch, now: now,
		gcProtect: cfg.GCProtect, gcExport: cfg.GCExportBranches, skipIndex: cfg.NoIndex}
}

// Branch 返回当前分支名。
func (r *Repo) Branch() string { return r.branch }

// Project 返回当前项目作用域名。
func (r *Repo) Project() string { return r.project }

// head 读取分支头;不存在返回 has=false。
func (r *Repo) head(ctx context.Context) (hash.Address, bool, error) {
	addr, err := r.st.BranchGet(ctx, r.project, r.branch)
	if errors.Is(err, store.ErrBranchNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return addr, true, nil
}

// loadSnapshot 读取并解码快照对象。
func (r *Repo) loadSnapshot(ctx context.Context, addr hash.Address) (*object.Snapshot, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	if kind != object.KindSnapshot {
		return nil, fmt.Errorf("repo: 地址 %s 是 %s,期望 snapshot", addr, kind)
	}
	return object.DecodeSnapshot(data)
}

// loadTree 读取并解码 tree 对象。
func (r *Repo) loadTree(ctx context.Context, addr hash.Address) (*object.Tree, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	if kind != object.KindTree {
		return nil, fmt.Errorf("repo: 地址 %s 是 %s,期望 tree", addr, kind)
	}
	return object.DecodeTree(data)
}

// treeAtSnapshot 返回某快照指向的 root tree。
func (r *Repo) treeAtSnapshot(ctx context.Context, addr hash.Address) (*object.Tree, error) {
	snap, err := r.loadSnapshot(ctx, addr)
	if err != nil {
		return nil, err
	}
	return r.loadTree(ctx, snap.Root)
}

// blobOf 读取 blob 原始字节。
func (r *Repo) blobOf(ctx context.Context, addr hash.Address) ([]byte, error) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	if kind != object.KindBlob {
		return nil, fmt.Errorf("repo: 地址 %s 是 %s,期望 blob", addr, kind)
	}
	return data, nil
}
