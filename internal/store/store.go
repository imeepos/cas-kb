// Package store 定义内容寻址存储接口与 Postgres 实现。
package store

import (
	"context"
	"errors"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// ErrNotFound 表示对象不存在。
var ErrNotFound = errors.New("store: 对象不存在")

// ErrBranchNotFound 表示分支不存在。
var ErrBranchNotFound = errors.New("store: 分支不存在")

// ErrProjectNotFound 表示项目不存在。
var ErrProjectNotFound = errors.New("store: 项目不存在")

// ObjectInfo 是全量扫描返回的单个对象摘要,供 GC / FSCK 使用。
type ObjectInfo struct {
	Addr hash.Address
	Kind object.Kind
	Size int
}

// BranchRef 是分支表的一行:项目内名字 → 快照地址,附命名空间描述。
type BranchRef struct {
	Project     string
	Name        string
	Addr        hash.Address
	Description string
}

// Store 是内容寻址存储 + 分支指针的最小契约。
// 对象写入幂等且全局共享;分支按项目划分命名空间,推进是唯一可变写路径。
type Store interface {
	Put(ctx context.Context, kind object.Kind, data []byte) (hash.Address, error)
	Get(ctx context.Context, addr hash.Address) ([]byte, object.Kind, error)
	Has(ctx context.Context, addr hash.Address) (bool, error)
	Delete(ctx context.Context, addr hash.Address) error
	List(ctx context.Context, fn func(ObjectInfo) error) error
	ProjectCreate(ctx context.Context, name, description string) error
	ProjectStats(ctx context.Context) ([]ProjectStat, error)
	ProjectDescribe(ctx context.Context, name, description string) error
	BranchGet(ctx context.Context, project, name string) (hash.Address, error)
	BranchSet(ctx context.Context, project, name string, addr hash.Address) error
	BranchDelete(ctx context.Context, project, name string) error
	BranchList(ctx context.Context, project string) ([]BranchRef, error)
	BranchListAll(ctx context.Context) ([]BranchRef, error)
	BranchDescribe(ctx context.Context, project, name, description string) error
	Close() error
}
