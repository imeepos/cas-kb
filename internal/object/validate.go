package object

import (
	"fmt"
	"strings"
)

// ValidateNote 校验 note 结构:标题与 body 必填、body 地址合法、schema 一致、links 合法。
func ValidateNote(n *Note) error {
	if n.Kind != KindNote {
		return errKind(n.Kind)
	}
	if strings.TrimSpace(n.Meta.Title) == "" {
		return fmt.Errorf("object: note 缺少标题")
	}
	if n.Meta.CreatedAt <= 0 {
		return fmt.Errorf("object: note created_at 非法")
	}
	if err := validateAddr(n.Body, "body"); err != nil {
		return err
	}
	for _, l := range n.Links {
		if strings.TrimSpace(l.Slug) == "" {
			return fmt.Errorf("object: note 链接缺少 slug")
		}
	}
	return ensureSchema(n.Meta.SchemaVersion)
}

// ValidateTree 校验 tree 结构:entries 的 slug 非空且唯一、类型合法(note|dir)、addr 合法。
func ValidateTree(t *Tree) error {
	if t.Kind != KindTree {
		return errKind(t.Kind)
	}
	seen := make(map[string]bool, len(t.Entries))
	for _, e := range t.Entries {
		if strings.TrimSpace(e.Slug) == "" {
			return fmt.Errorf("object: tree 条目的 slug 为空")
		}
		if seen[e.Slug] {
			return fmt.Errorf("object: tree 出现重复 slug %q", e.Slug)
		}
		seen[e.Slug] = true
		if !IsValidEntryType(e.Type) {
			return fmt.Errorf("object: tree 条目 %q 的类型 %q 非法(期望 note|dir)", e.Slug, e.Type)
		}
		if err := validateAddr(e.Addr, "entry addr"); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSnapshot 校验 snapshot 结构:root 必填、parents 合法。
func ValidateSnapshot(s *Snapshot) error {
	if s.Kind != KindSnapshot {
		return errKind(s.Kind)
	}
	if err := validateAddr(s.Root, "root"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range s.Parents {
		if err := validateAddr(p, "parent"); err != nil {
			return err
		}
		if seen[string(p)] {
			return fmt.Errorf("object: snapshot 父母重复 %s", p)
		}
		seen[string(p)] = true
	}
	if s.Time <= 0 {
		return fmt.Errorf("object: snapshot time 非法")
	}
	return nil
}

func validateAddr(a Address, what string) error {
	if a == "" {
		return fmt.Errorf("object: %s 地址为空", what)
	}
	if err := Validate(a); err != nil {
		return fmt.Errorf("object: %s 地址非法 %s: %w", what, a, err)
	}
	return nil
}
