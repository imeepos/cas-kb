package object

// NewTree 返回一个空的 tree 对象。
func NewTree() *Tree { return &Tree{Kind: KindTree, Entries: []TreeEntry{}} }

// Clone 返回 tree 的深拷贝,避免共享底层切片。
func (t *Tree) Clone() *Tree {
	cp := *t
	cp.Entries = append([]TreeEntry(nil), t.Entries...)
	return &cp
}

// Get 按 slug 查找条目地址。
func (t *Tree) Get(slug string) (Address, bool) {
	for _, e := range t.Entries {
		if e.Slug == slug {
			return e.Addr, true
		}
	}
	return "", false
}

// Set 设置或覆盖一条目地址。slug 已存在时替换值。
func (t *Tree) Set(slug string, addr Address) {
	for i := range t.Entries {
		if t.Entries[i].Slug == slug {
			t.Entries[i].Addr = addr
			return
		}
	}
	t.Entries = append(t.Entries, TreeEntry{Slug: slug, Addr: addr})
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
