// Package object 定义四类内容寻址对象:blob / note / tree / snapshot,
// 及规范编解码与结构校验。规范编码保证同一逻辑对象在任何机器上逐字节一致。
package object

import (
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
)

// Kind 是对象类型标签。
type Kind string

// 四类对象类型常量。
const (
	KindBlob     Kind = "blob"
	KindNote     Kind = "note"
	KindTree     Kind = "tree"
	KindSnapshot Kind = "snapshot"
)

// SchemaVersion 是当前结构化对象(meta)的格式版本。字段集合变更必须升级此值。
const SchemaVersion = 1

// validKinds 声明所有合法 kind。
var validKinds = map[Kind]bool{
	KindBlob:     true,
	KindNote:     true,
	KindTree:     true,
	KindSnapshot: true,
}

// IsValidKind 报告 kind 是否合法。
func IsValidKind(k Kind) bool { return validKinds[k] }

// ErrBadKind 表示对象载荷携带的类型标签非法。
var ErrBadKind = errors.New("object: 非法 kind")

// Meta 是 note 的元数据,字段声明序即规范字节序。
type Meta struct {
	Title         string   `json:"title"`
	Tags          []string `json:"tags,omitempty"`
	CreatedAt     int64    `json:"created_at"`
	SchemaVersion int      `json:"schema_version"`
}

// Link 是 note 指向其他条目的链接,只存 slug。
type Link struct {
	Slug string `json:"slug"`
	Rel  string `json:"rel,omitempty"`
}

// Note 是一条知识条目节点。
type Note struct {
	Kind  Kind    `json:"kind"`
	Meta  Meta    `json:"meta"`
	Body  Address `json:"body"`
	Links []Link  `json:"links,omitempty"`
}

// TreeEntry 是树中的一个映射项:slug -> note 地址。
type TreeEntry struct {
	Slug string  `json:"slug"`
	Addr Address `json:"addr"`
}

// Tree 是目录/解析表:扁平一层 slug -> note 地址。
type Tree struct {
	Kind    Kind        `json:"kind"`
	Entries []TreeEntry `json:"entries"`
}

// Snapshot 是全库一个版本的命名:root tree 地址 + 历史父母 + 时间与消息。
type Snapshot struct {
	Kind    Kind      `json:"kind"`
	Root    Address   `json:"root"`
	Parents []Address `json:"parents,omitempty"`
	Time    int64     `json:"time"`
	Message string    `json:"message"`
}

// Address 是内容寻址地址的别名,便于对象层引用。
type Address = hash.Address

// Sum 计算规范化字节的地址。
var Sum = hash.Sum

// Validate 校验地址格式。
var Validate = hash.Validate

func errKind(k Kind) error { return fmt.Errorf("object: 期望对象 kind %q", k) }
