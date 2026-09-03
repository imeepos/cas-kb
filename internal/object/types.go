// Package object 定义内容寻址对象:blob / note / tree / snapshot /
// indexroot / indexshard(M4 检索)/ vecroot / vecshard(M6-A 语义向量),
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
	KindBlob       Kind = "blob"
	KindNote       Kind = "note"
	KindTree       Kind = "tree"
	KindSnapshot   Kind = "snapshot"
	KindIndexRoot  Kind = "indexroot"  // M4 检索索引根
	KindIndexShard Kind = "indexshard" // M4 倒排索引分片
	KindVecRoot    Kind = "vecroot"    // M6-A 语义向量索引根
	KindVecShard   Kind = "vecshard"   // M6-A 语义向量分片
)

// SchemaVersion 是当前结构化对象(meta)的格式版本。字段集合变更必须升级此值。
const SchemaVersion = 1

// validKinds 声明所有合法 kind。
var validKinds = map[Kind]bool{
	KindBlob:       true,
	KindNote:       true,
	KindTree:       true,
	KindSnapshot:   true,
	KindIndexRoot:  true,
	KindIndexShard: true,
	KindVecRoot:    true,
	KindVecShard:   true,
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

// EntryType 是 tree 条目的目标类型:M3.8 起目录可嵌套,
// 条目指向 note(知识条目)或 dir(子目录,即另一棵 tree)。
type EntryType string

// 两类条目类型常量。
const (
	EntryNote EntryType = "note"
	EntryDir  EntryType = "dir"
)

// validEntryTypes 声明所有合法条目类型。
var validEntryTypes = map[EntryType]bool{
	EntryNote: true,
	EntryDir:  true,
}

// IsValidEntryType 报告条目类型是否合法。
func IsValidEntryType(t EntryType) bool { return validEntryTypes[t] }

// TreeEntry 是树中的一个映射项:slug -> 目标地址。
// type=note 时 addr 指向 note 对象;type=dir 时指向子 tree 对象(嵌套目录)。
type TreeEntry struct {
	Slug string    `json:"slug"`
	Type EntryType `json:"type"`
	Addr Address   `json:"addr"`
}

// Tree 是目录:一层 slug -> 条目(note 或子目录)。
type Tree struct {
	Kind    Kind        `json:"kind"`
	Entries []TreeEntry `json:"entries"`
}

// Snapshot 是全库一个版本的命名:root tree 地址 + 历史父母 + 时间与消息。
// Index(M4)指向检索索引根对象;为空表示该快照未建索引(旧数据,可全量重建)。
// Vec(M6-A)指向语义向量索引根(vecroot)对象;为空表示该快照未建向量
// (旧数据或内容变更后未重跑 kb index rebuild --embed,可全量重建)。
// 两个 omitempty 保证无索引/无向量快照的编码与之前逐字节一致(对象地址不变)。
type Snapshot struct {
	Kind    Kind      `json:"kind"`
	Root    Address   `json:"root"`
	Parents []Address `json:"parents,omitempty"`
	Time    int64     `json:"time"`
	Message string    `json:"message"`
	Index   Address   `json:"index,omitempty"`
	Vec     Address   `json:"vec,omitempty"`
}

// IndexDoc 是索引文档表的一行:note 地址 → 路径与加权文档长度(BM25 用)。
type IndexDoc struct {
	Addr Address `json:"a"`
	Path string  `json:"p"`
	Len  int     `json:"l"`
}

// IndexRoot 是检索索引根:固定槽位分片地址表(以桶号为下标,空桶为空串)
// + 文档表(按地址排序)。Version 是索引格式版本,格式演进时升级。
type IndexRoot struct {
	Kind    Kind       `json:"kind"`
	Version int        `json:"version"`
	Shards  []Address  `json:"shards"`
	Docs    []IndexDoc `json:"docs"`
}

// IndexPosting 是一个词元对一篇笔记的倒排项:标题/标签/正文各字段词频。
type IndexPosting struct {
	Addr  Address `json:"a"`
	Title int     `json:"t"`
	Tags  int     `json:"g"`
	Body  int     `json:"b"`
}

// IndexShard 是一个倒排分片:词元 → 倒排项列表(项按 note 地址排序)。
type IndexShard struct {
	Kind     Kind                      `json:"kind"`
	Bucket   int                       `json:"bucket"`
	Postings map[string][]IndexPosting `json:"postings"`
}

// VecItem 是 vecshard 中的一条向量项:笔记全路径 → 嵌入向量。
// Vec 是全部 float32 分量按 little-endian 拼接后再 base64(StdEncoding)
// 的单个字符串——二进制承载避免 JSON 浮点文本的精度/格式歧义,
// 保证跨平台逐字节确定(见 vector.go 的 EncodeVecBase64)。
type VecItem struct {
	Path string `json:"path"`
	Vec  string `json:"vec"`
}

// VecShard 是一个语义向量分片(M6-A,DESIGN §7.3):固定桶号下全部笔记的
// 嵌入向量;桶号 = FNV-1a(条目全路径) % 64,与 indexshard 同构分片。
// Model/Dim 内嵌于内容:同一批向量换模型/维度重建必然产出不同地址
// (向量按 model_id 版本化入内容,跨模型不共享)。
type VecShard struct {
	Kind  Kind      `json:"kind"`
	Model string    `json:"model"`
	Dim   int       `json:"dim"`
	Items []VecItem `json:"items"`
}

// VecRoot 是语义向量索引根(M6-A):声明生成向量的模型与维度,并按桶号
// 列出各 vecshard 地址(以桶号为下标,空桶为空串,照 indexroot 范本)。
// fsck 以 root 的 model/dim 为基准校验各分片一致性。
type VecRoot struct {
	Kind   Kind      `json:"kind"`
	Model  string    `json:"model"`
	Dim    int       `json:"dim"`
	Shards []Address `json:"shards"`
}

// Address 是内容寻址地址的别名,便于对象层引用。
type Address = hash.Address

// Sum 计算规范化字节的地址。
var Sum = hash.Sum

// Validate 校验地址格式。
var Validate = hash.Validate

func errKind(k Kind) error { return fmt.Errorf("object: 期望对象 kind %q", k) }
