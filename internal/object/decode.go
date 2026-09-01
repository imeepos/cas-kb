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

// DecodeTree 解析 tree 对象。
func DecodeTree(data []byte) (*Tree, error) {
	var t Tree
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("object: tree 解码失败: %w", err)
	}
	if t.Kind != KindTree {
		return nil, fmt.Errorf("object: tree 载荷 kind 为 %q,期望 %q", t.Kind, KindTree)
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

// ensureSchema 拒绝不支持的 schema_version。
func ensureSchema(v int) error {
	if v != SchemaVersion {
		return fmt.Errorf("object: 不支持的 schema_version %d(期望 %d),字段格式已变更", v, SchemaVersion)
	}
	return nil
}
