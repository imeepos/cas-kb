package object

import (
	"encoding/json"
	"fmt"
)

// Decode 按 kind 解码对象字节:blob 原样;其余解析规范 JSON 并校验 kind 标签。
// 返回的 any 是 *Note / *Tree / *Snapshot / []byte 之一。
func Decode(k Kind, data []byte) (any, error) {
	switch k {
	case KindBlob:
		return data, nil
	case KindNote:
		return DecodeNote(data)
	case KindTree:
		return DecodeTree(data)
	case KindSnapshot:
		return DecodeSnapshot(data)
	case KindIndexRoot:
		return DecodeIndexRoot(data)
	case KindIndexShard:
		return DecodeIndexShard(data)
	case KindVecRoot:
		return DecodeVecRoot(data)
	case KindVecShard:
		return DecodeVecShard(data)
	default:
		return nil, fmt.Errorf("object: 未知 kind %q", k)
	}
}

// DecodeNote 解析 note 对象并校验 kind 与 schema_version。
func DecodeNote(data []byte) (*Note, error) {
	var n Note
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("object: note 解码失败: %w", err)
	}
	if n.Kind != KindNote {
		return nil, fmt.Errorf("object: note 载荷 kind 为 %q,期望 %q", n.Kind, KindNote)
	}
	if err := ensureSchema(n.Meta.SchemaVersion); err != nil {
		return nil, err
	}
	return &n, nil
}

// DecodeTree 解析 tree 对象。条目类型必须合法(v4 起强制;
// v3 旧格式条目无 type 字段,在此被响亮拒绝)。
func DecodeTree(data []byte) (*Tree, error) {
	var t Tree
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("object: tree 解码失败: %w", err)
	}
	if t.Kind != KindTree {
		return nil, fmt.Errorf("object: tree 载荷 kind 为 %q,期望 %q", t.Kind, KindTree)
	}
	for _, e := range t.Entries {
		if !IsValidEntryType(e.Type) {
			return nil, fmt.Errorf("object: tree 条目 %q 的类型 %q 非法(期望 note|dir);"+
				"可能是 v3 旧格式数据,请清库重建", e.Slug, e.Type)
		}
	}
	return &t, nil
}

// DecodeSnapshot 解析 snapshot 对象。
func DecodeSnapshot(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("object: snapshot 解码失败: %w", err)
	}
	if s.Kind != KindSnapshot {
		return nil, fmt.Errorf("object: snapshot 载荷 kind 为 %q,期望 %q", s.Kind, KindSnapshot)
	}
	return &s, nil
}

// DecodeIndexRoot 解析检索索引根对象。
func DecodeIndexRoot(data []byte) (*IndexRoot, error) {
	var ir IndexRoot
	if err := json.Unmarshal(data, &ir); err != nil {
		return nil, fmt.Errorf("object: indexroot 解码失败: %w", err)
	}
	if ir.Kind != KindIndexRoot {
		return nil, fmt.Errorf("object: indexroot 载荷 kind 为 %q,期望 %q", ir.Kind, KindIndexRoot)
	}
	return &ir, nil
}

// DecodeIndexShard 解析检索索引分片对象。
func DecodeIndexShard(data []byte) (*IndexShard, error) {
	var s IndexShard
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("object: indexshard 解码失败: %w", err)
	}
	if s.Kind != KindIndexShard {
		return nil, fmt.Errorf("object: indexshard 载荷 kind 为 %q,期望 %q", s.Kind, KindIndexShard)
	}
	return &s, nil
}

// DecodeVecShard 解析语义向量分片对象;载荷 kind 标签必须精确匹配
// (v6 门禁:拒绝一切不认识/不匹配的编码,同 M4 先例)。
func DecodeVecShard(data []byte) (*VecShard, error) {
	var s VecShard
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("object: vecshard 解码失败: %w", err)
	}
	if s.Kind != KindVecShard {
		return nil, fmt.Errorf("object: vecshard 载荷 kind 为 %q,期望 %q", s.Kind, KindVecShard)
	}
	return &s, nil
}

// DecodeVecRoot 解析语义向量索引根对象;载荷 kind 标签必须精确匹配。
func DecodeVecRoot(data []byte) (*VecRoot, error) {
	var vr VecRoot
	if err := json.Unmarshal(data, &vr); err != nil {
		return nil, fmt.Errorf("object: vecroot 解码失败: %w", err)
	}
	if vr.Kind != KindVecRoot {
		return nil, fmt.Errorf("object: vecroot 载荷 kind 为 %q,期望 %q", vr.Kind, KindVecRoot)
	}
	return &vr, nil
}

// ensureSchema 拒绝不支持的 schema_version。
func ensureSchema(v int) error {
	if v != SchemaVersion {
		return fmt.Errorf("object: 不支持的 schema_version %d(期望 %d),字段格式已变更", v, SchemaVersion)
	}
	return nil
}
