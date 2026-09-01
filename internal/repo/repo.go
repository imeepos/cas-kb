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
	// Branch 是默认工作分支名,默认 "main"。
	Branch string
	// Now 注入时间源,便于测试;为空时用 time.Now。
	Now func() int64
}

// Repo 是一个已打开的知识库仓库。依赖 store,不反向依赖。
type Repo struct {
	st     store.Store
	branch string
	now    func() int64
}

// Open 构造仓库。Store 由调用方打开并负责 Close。
func Open(s store.Store, cfg Config) *Repo {
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	now := cfg.Now
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}
	return &Repo{st: s, branch: branch, now: now}
}

// Branch 返回当前分支名。
func (r *Repo) Branch() string { return r.branch }

// head 读取分支头;不存在返回 has=false。
func (r *Repo) head(ctx context.Context) (hash.Address, bool, error) {
	addr, err := r.st.BranchGet(ctx, r.branch)
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
