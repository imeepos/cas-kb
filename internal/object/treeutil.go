package object

// NewTree 返回一个空的 tree 对象。
func NewTree() *Tree { return &Tree{Kind: KindTree, Entries: []TreeEntry{}} }

// Clone 返回 tree 的深拷贝,避免共享底层切片。
func (t *Tree) Clone() *Tree {
	cp := *t
	cp.Entries = append([]TreeEntry(nil), t.Entries...)
	return &cp
}

// Lookup 按 slug 查找条目,返回完整条目(含类型)。
func (t *Tree) Lookup(slug string) (TreeEntry, bool) {
	for _, e := range t.Entries {
		if e.Slug == slug {
			return e, true
		}
	}
	return TreeEntry{}, false
}

// Set 设置或覆盖一条目(slug + 类型 + 地址)。slug 已存在时整体替换。
func (t *Tree) Set(slug string, typ EntryType, addr Address) {
	for i := range t.Entries {
		if t.Entries[i].Slug == slug {
			t.Entries[i].Type = typ
			t.Entries[i].Addr = addr
			return
		}
	}
	t.Entries = append(t.Entries, TreeEntry{Slug: slug, Type: typ, Addr: addr})
}

// Delete 移除一条目;不存在时为空操作。
func (t *Tree) Delete(slug string) {
	for i := range t.Entries {
		if t.Entries[i].Slug == slug {
			t.Entries = append(t.Entries[:i], t.Entries[i+1:]...)
			return
		}
	}
}
