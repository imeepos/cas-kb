package repo

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// M5-A 批次测试(docs/research/merge-design.md §2/§3/§5):
// LCA(含多 LCA 蟹状历史)、判定表逐类、Merkle 剪枝、形态冲突、双亲快照
// 兼容性(fsck/GC/backup/pull/reset)、本地领先空操作、冲突清单完整性。
// 一律临时 SQLite 库(testdb.NewSQLite 派生),零外部依赖。

// fakeAddr 生成格式合法的伪地址(根层判定不读条目对象,无需真实落库)。
func fakeAddr(seed string) hash.Address { return hash.Sum([]byte("merge-test:" + seed)) }

// mkTree 构造一棵内存树(条目地址须格式合法)。
func mkTree(entries ...object.TreeEntry) *object.Tree {
	t := object.NewTree()
	for _, e := range entries {
		t.Set(e.Slug, e.Type, e.Addr)
	}
	return t
}

func noteEntry(slug, seed string) object.TreeEntry {
	return object.TreeEntry{Slug: slug, Type: object.EntryNote, Addr: fakeAddr(seed)}
}

// treeMap 把树转成 slug → 条目映射,便于断言。
func treeMap(t *object.Tree) map[string]object.TreeEntry {
	m := map[string]object.TreeEntry{}
	for _, e := range t.Entries {
		m[e.Slug] = e
	}
	return m
}

// mergePair 开一对「两机」仓库:A=ours(本地库),B=theirs(独立远端库)。
func mergePair(t *testing.T, now int64) (a, b *Repo, aSt, bSt store.Store) {
	t.Helper()
	a, aSt, _ = newRepo(t, "main")
	bSt = openRemote(t)
	b = Open(bSt, Config{Branch: "main", Now: func() int64 { return now }})
	return a, b, aSt, bSt
}

// seedCommon 在两库间建立公共基点:A 落基线条目,B fast-forward 拉平。
// 返回基点快照地址与基线条目地址表。
func seedCommon(t *testing.T, a *Repo, aSt store.Store, b *Repo) (hash.Address, map[string]hash.Address) {
	t.Helper()
	ctx := context.Background()
	addrs := map[string]hash.Address{}
	for _, p := range []struct{ path, title, body string }{
		{"owner", "库主人", "owner body"},
		{"workflow/inbox", "收件箱", "收件箱基线"},
		{"workflow/daily", "每日站会纪要", "daily body"},
		{"go", "Go 并发备忘", "go 基线"},
	} {
		_, noteAddr, err := a.SetNote(ctx, p.path, NoteInput{Title: p.title, Body: p.body, Time: fixedTime}, "seed "+p.path)
		if err != nil {
			t.Fatal(err)
		}
		addrs[p.path] = noteAddr
	}
	s0, _, err := a.SetNote(ctx, "seed", NoteInput{Title: "Seed", Body: "seed body", Time: fixedTime}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	return s0, addrs
}

// mergeScenario 汇总一次两机演算的关键地址。
type mergeScenario struct {
	a, b         *Repo
	aSt, bSt     store.Store
	s0           hash.Address
	oursHead     hash.Address
	theirsHead   hash.Address
	baseNoteAddr map[string]hash.Address
	inboxOurs    hash.Address // ours 侧 inbox 条目地址(仅冲突场景非空)
}

// runMergeScenario 构造调研 §3.2/§3.4 的两机演算:
// 双方独立把 go 改成同一内容(双侧同变),ours 加 kb,theirs 删 workflow/daily;
// withInboxConflict 时双方再把 workflow/inbox 改成不同内容(双侧异改冲突)。
func runMergeScenario(t *testing.T, now int64, withInboxConflict bool) *mergeScenario {
	t.Helper()
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, now)
	s0, baseAddrs := seedCommon(t, a, aSt, b)
	sc := &mergeScenario{a: a, b: b, aSt: aSt, bSt: bSt, s0: s0, baseNoteAddr: baseAddrs}
	goInput := NoteInput{Title: "Go 并发备忘", Body: "go 基线\nchannel 关闭后 range 退出", Time: fixedTime}
	if _, _, err := a.SetNote(ctx, "go", goInput, "ours go"); err != nil {
		t.Fatal(err)
	}
	if withInboxConflict {
		_, noteAddr, err := a.SetNote(ctx, "workflow/inbox", NoteInput{Title: "收件箱", Body: "收件箱 周三评审", Time: fixedTime}, "ours inbox")
		if err != nil {
			t.Fatal(err)
		}
		sc.inboxOurs = noteAddr
	}
	if _, _, err := a.SetNote(ctx, "kb", NoteInput{Title: "KB", Body: "kb body", Time: fixedTime}, "ours kb"); err != nil {
		t.Fatal(err)
	}
	head, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	sc.oursHead = head
	if _, _, err := b.SetNote(ctx, "go", goInput, "theirs go"); err != nil {
		t.Fatal(err)
	}
	if withInboxConflict {
		if _, _, err := b.SetNote(ctx, "workflow/inbox", NoteInput{Title: "收件箱", Body: "收件箱 周四评审", Time: fixedTime}, "theirs inbox"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.RemoveNote(ctx, "workflow/daily", "theirs rm daily"); err != nil {
		t.Fatal(err)
	}
	tHead, err := bSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	sc.theirsHead = tHead
	return sc
}

// countSnapshotsWithTwoParents 扫描全库统计双亲快照数(冲突即停断言用)。
func countSnapshotsWithTwoParents(t *testing.T, s store.Store) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	if err := s.List(ctx, func(info store.ObjectInfo) error {
		if info.Kind != object.KindSnapshot {
			return nil
		}
		data, kind, err := s.Get(ctx, info.Addr)
		if err != nil {
			return err
		}
		if kind != object.KindSnapshot {
			return nil
		}
		snap, err := object.DecodeSnapshot(data)
		if err != nil {
			return nil
		}
		if len(snap.Parents) == 2 {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// parentsSet 读快照父母为集合(编码按地址排序,断言用集合语义)。
func parentsSet(t *testing.T, r *Repo, addr hash.Address) map[hash.Address]bool {
	t.Helper()
	snap, err := r.loadSnapshot(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	m := map[hash.Address]bool{}
	for _, p := range snap.Parents {
		m[p] = true
	}
	return m
}

// TestMergeLCAUniqueBase:链式历史唯一基准;显式指定候选之一可用。
func TestMergeLCAUniqueBase(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	s0, _ := seedCommon(t, a, aSt, b)
	if _, _, err := a.SetNote(ctx, "a1", NoteInput{Title: "A1", Body: "a1", Time: fixedTime}, "a1"); err != nil {
		t.Fatal(err)
	}
	o2, _, err := a.SetNote(ctx, "a2", NoteInput{Title: "A2", Body: "a2", Time: fixedTime}, "a2")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "b1", NoteInput{Title: "B1", Body: "b1", Time: fixedTime}, "b1"); err != nil {
		t.Fatal(err)
	}
	bHead, err := bSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	// MergeBase 契约:两侧头的对象链都已在本地(Merge 流程先传输;此处手动补齐)
	tx := &transfer{st: aSt, src: bSt, seen: map[string]bool{}}
	if err := tx.copy(ctx, bHead); err != nil {
		t.Fatal(err)
	}
	mb, err := a.MergeBase(ctx, o2, bHead, "")
	if err != nil {
		t.Fatal(err)
	}
	if mb.Base != s0 {
		t.Fatalf("链式历史基准应为 s0,got %s", mb.Base)
	}
	if len(mb.Candidates) != 1 || mb.Candidates[0] != s0 {
		t.Fatalf("候选应恰为 s0: %v", mb.Candidates)
	}
	mb2, err := a.MergeBase(ctx, o2, bHead, string(s0))
	if err != nil || mb2.Base != s0 {
		t.Fatalf("显式基准应接受 s0: %v %v", mb2, err)
	}
	if _, err := a.MergeBase(ctx, o2, bHead, string(o2)); err == nil {
		t.Fatal("显式基准为 ours 自身应被拒绝(不是共同祖先候选)")
	}
}

// TestMergeLCARejectsNoCommonHistory:两库独立 init 无共同祖先,响亮拒绝。
func TestMergeLCARejectsNoCommonHistory(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	if _, _, err := a.SetNote(ctx, "a", NoteInput{Title: "A", Body: "a", Time: fixedTime}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "b", NoteInput{Title: "B", Body: "b", Time: fixedTime}, "b"); err != nil {
		t.Fatal(err)
	}
	oHead, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	tHead, err := bSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	// MergeBase 契约:两侧头的对象链都已在本地(Merge 流程先传输;此处手动补齐)
	tx := &transfer{st: aSt, src: bSt, seen: map[string]bool{}}
	if err := tx.copy(ctx, tHead); err != nil {
		t.Fatal(err)
	}
	if _, err := a.MergeBase(ctx, oHead, tHead, ""); !errors.Is(err, ErrMergeNoCommonHistory) {
		t.Fatalf("无共同历史应报 ErrMergeNoCommonHistory,got %v", err)
	}
	if _, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{}); !errors.Is(err, ErrMergeNoCommonHistory) {
		t.Fatalf("Merge 无共同历史应响亮拒绝,got %v", err)
	}
}

// TestMergeLCAMultipleBasesCrabHistory:蟹状历史(两次合并并存)检出多候选
// → 方案甲:响亮拒绝并列出全部候选;显式指定其一可继续(零树差异仍落快照)。
func TestMergeLCAMultipleBasesCrabHistory(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	seedCommon(t, a, aSt, b)
	if _, _, err := a.SetNote(ctx, "a1", NoteInput{Title: "A1", Body: "a1", Time: fixedTime}, "a1"); err != nil {
		t.Fatal(err)
	}
	o1, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.SetNote(ctx, "b1", NoteInput{Title: "B1", Body: "b1", Time: fixedTime}, "b1"); err != nil {
		t.Fatal(err)
	}
	t1, err := bSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	// A 合并 B → M1[o1, t1](真实 API)
	resA, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatalf("第一次合并应零冲突: %v", err)
	}
	m1 := resA.Snap
	// 模拟 B 侧「并发合并」:B 库补齐 o1 链对象,注入 M2[t1, o1]。
	// (顺序测试中两次 Merge 无法互见合并前头;直接构造两机同时
	//   pull --merge 的落库结果,对象层允许,DAG 形态与真实并发一致)
	tx := &transfer{st: bSt, src: aSt, seen: map[string]bool{}}
	if err := tx.copy(ctx, o1); err != nil {
		t.Fatal(err)
	}
	o1Snap, err := a.loadSnapshot(ctx, o1)
	if err != nil {
		t.Fatal(err)
	}
	m2Data, err := object.EncodeSnapshot(&object.Snapshot{
		Kind: object.KindSnapshot, Root: o1Snap.Root,
		Parents: []hash.Address{t1, o1}, Time: fixedTime + 1, Message: "concurrent merge",
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := bSt.Put(ctx, object.KindSnapshot, m2Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := bSt.BranchSet(ctx, "default", "main", m2); err != nil {
		t.Fatal(err)
	}
	// A 再合并 B:o1、t1 均为最深公共祖先 → 多 LCA 响亮拒绝,候选齐全
	_, err = a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	var multi *ErrMergeMultipleBases
	if !errors.As(err, &multi) {
		t.Fatalf("蟹状历史应报 ErrMergeMultipleBases,got %v", err)
	}
	if len(multi.Candidates) != 2 {
		t.Fatalf("应检出 2 个候选,got %v", multi.Candidates)
	}
	cand := map[hash.Address]bool{}
	for _, c := range multi.Candidates {
		cand[c] = true
	}
	if !cand[o1] || !cand[t1] {
		t.Fatalf("候选应恰为 o1 与 t1: %v", multi.Candidates)
	}
	// 显式指定候选之一(o1)→ 合并可继续;零树差异仍落合并快照(保拓扑)
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{Base: string(o1), Message: "merge crab"})
	if err != nil {
		t.Fatalf("显式基准后应可合并: %v", err)
	}
	if res.Base != o1 {
		t.Fatalf("基准应为 o1,got %s", res.Base)
	}
	if res.Snap == "" {
		t.Fatal("零树差异仍应落合并快照(保持两链可达)")
	}
	if got := parentsSet(t, a, res.Snap); !got[m1] || !got[m2] || len(got) != 2 {
		t.Fatalf("合并快照父母应为 [M1, M2]: %v", got)
	}
	fs, err := a.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Problems) != 0 {
		t.Fatalf("合并后 fsck 应零问题: %v", fs.Problems)
	}
}

// TestMergeDecisionUnchanged:行 1 三侧相同 → 零成本跳过,整树复用。
func TestMergeDecisionUnchanged(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "merge_dec_unchanged")
	base := mkTree(noteEntry("x", "x1"), noteEntry("y", "y1"))
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, base, base, base, fakeAddr("root"), nil, out)
	if err != nil {
		t.Fatal(err)
	}
	if node.dirty {
		t.Fatal("三侧相同不应产生任何变更")
	}
	if node.addr != fakeAddr("root") {
		t.Fatalf("应原样复用 ours 子树地址: %s", node.addr)
	}
	if out.autoMerged != 0 || len(out.conflicts) != 0 {
		t.Fatalf("未变不应计数或报冲突: %d %v", out.autoMerged, out.conflicts)
	}
}

// TestMergeDecisionOneSide:行 2/3 单侧变(增/改/删)取单侧。
func TestMergeDecisionOneSide(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "merge_dec_oneside")
	base := mkTree(noteEntry("x", "x1"), noteEntry("y", "y1"))
	ours := mkTree(noteEntry("x", "x1"), noteEntry("y", "y1"), noteEntry("z", "z1"))
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, base, ours, base.Clone(), fakeAddr("ours"), nil, out)
	if err != nil {
		t.Fatal(err)
	}
	if node.dirty {
		t.Fatal("ours 单侧变:结果与 ours 一致,树不应有脏")
	}
	if out.autoMerged != 1 {
		t.Fatalf("ours 单侧增应计 1 次自动合并,got %d", out.autoMerged)
	}
	theirs := mkTree(noteEntry("x", "x2"), noteEntry("w", "w1"))
	out2 := &mergeOutput{}
	node2, err := r.mergeTrees(ctx, base, base.Clone(), theirs, fakeAddr("base"), nil, out2)
	if err != nil {
		t.Fatal(err)
	}
	if !node2.dirty {
		t.Fatal("theirs 单侧变应产生合成树")
	}
	m := treeMap(node2.tree)
	if len(m) != 2 || m["x"].Addr != fakeAddr("x2") || m["w"].Addr != fakeAddr("w1") {
		t.Fatalf("应取 theirs 的改/增: %v", m)
	}
	if _, ok := m["y"]; ok {
		t.Fatal("theirs 单侧删除的 y 不应出现在合并树")
	}
	if out2.autoMerged != 3 || len(out2.conflicts) != 0 {
		t.Fatalf("theirs 三处单侧变应计 3 且无冲突: %d %v", out2.autoMerged, out2.conflicts)
	}
}

// TestMergeDecisionBothSame:行 4 双侧同变(含双侧同删)同地址自动合。
func TestMergeDecisionBothSame(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "merge_dec_bothsame")
	base := mkTree(noteEntry("x", "x1"), noteEntry("y", "y1"))
	ours := mkTree(noteEntry("x", "x2"), noteEntry("y", "y1"))
	theirs := mkTree(noteEntry("x", "x2"), noteEntry("y", "y1"))
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, base, ours, theirs, fakeAddr("ours"), nil, out)
	if err != nil {
		t.Fatal(err)
	}
	if node.dirty {
		t.Fatal("双侧同变同地址:结果与 ours 一致,树不应有脏")
	}
	if out.autoMerged != 1 || len(out.conflicts) != 0 {
		t.Fatalf("双侧同变应计 1 且无冲突: %d %v", out.autoMerged, out.conflicts)
	}
	base2 := mkTree(noteEntry("x", "x1"), noteEntry("y", "y1"))
	ours2 := mkTree(noteEntry("x", "x1"))
	theirs2 := mkTree(noteEntry("x", "x1"))
	out2 := &mergeOutput{}
	node2, err := r.mergeTrees(ctx, base2, ours2, theirs2, fakeAddr("ours2"), nil, out2)
	if err != nil {
		t.Fatal(err)
	}
	if node2.dirty || out2.autoMerged != 1 || len(out2.conflicts) != 0 {
		t.Fatalf("双侧同删应自动合且计 1: dirty=%v n=%d c=%v", node2.dirty, out2.autoMerged, out2.conflicts)
	}
}

// TestMergeDecisionConflict:行 5 双侧异改(含 add/add)→ content 冲突,
// 树取 ours 占位;纯函数性:合并计算零写入。
func TestMergeDecisionConflict(t *testing.T) {
	ctx := context.Background()
	r, st, _ := newRepo(t, "merge_dec_conflict")
	base := mkTree(noteEntry("x", "x1"))
	ours := mkTree(noteEntry("x", "x2"))
	theirs := mkTree(noteEntry("x", "x3"))
	count := func() int {
		n := 0
		if err := st.List(ctx, func(store.ObjectInfo) error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, base, ours, theirs, fakeAddr("ours"), nil, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.conflicts) != 1 {
		t.Fatalf("应恰 1 条冲突: %v", out.conflicts)
	}
	c := out.conflicts[0]
	if c.Path != "x" || c.Kind != mergeKindContent ||
		c.Base != fakeAddr("x1") || c.Ours != fakeAddr("x2") || c.Theirs != fakeAddr("x3") {
		t.Fatalf("冲突四元组不符: %+v", c)
	}
	if treeMap(node.tree)["x"].Addr != fakeAddr("x2") {
		t.Fatal("冲突条目应取 ours 占位")
	}
	if out.autoMerged != 0 {
		t.Fatalf("冲突不应计入自动合并: %d", out.autoMerged)
	}
	if after := count(); after != before {
		t.Fatalf("纯函数合并不应写任何对象: before=%d after=%d", before, after)
	}
	out2 := &mergeOutput{}
	empty := object.NewTree()
	node2, err := r.mergeTrees(ctx, empty, mkTree(noteEntry("n", "n1")), mkTree(noteEntry("n", "n2")), fakeAddr("ao"), nil, out2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.conflicts) != 1 || out2.conflicts[0].Kind != mergeKindContent || out2.conflicts[0].Base != "" {
		t.Fatalf("add/add 应为 content 冲突且 base 为空: %+v", out2.conflicts)
	}
	if treeMap(node2.tree)["n"].Addr != fakeAddr("n1") {
		t.Fatal("add/add 冲突树中应取 ours 占位")
	}
}

// mergeCountStore 统计 tree 对象读取数(Merkle 剪枝以读取计数断言)。
type mergeCountStore struct {
	store.Store
	treeGets int
}

func (c *mergeCountStore) Get(ctx context.Context, addr hash.Address) ([]byte, object.Kind, error) {
	data, kind, err := c.Store.Get(ctx, addr)
	if err == nil && kind == object.KindTree {
		c.treeGets++
	}
	return data, kind, err
}

// TestMergeSubtreePruneZeroDrill:Merkle 地址相等即整棵子树剪枝,零下钻;
// 对照组:双侧目录异变必须下钻,恰读 base/ours/theirs 三棵子树。
func TestMergeSubtreePruneZeroDrill(t *testing.T) {
	ctx := context.Background()
	// build 在同一库内模拟两机:基线 → ours 改 mine/x(及可选 docs)→
	// 回退基线 → theirs 改 docs/a;返回三棵根树与计数 store。
	build := func(t *testing.T, oursDocs bool) (*mergeCountStore, *Repo, *object.Tree, *object.Tree, *object.Tree, hash.Address) {
		st, _ := freshStore(t)
		cs := &mergeCountStore{Store: st}
		r := Open(cs, Config{Branch: "main", Now: func() int64 { return fixedTime }})
		if _, _, err := r.SetNote(ctx, "keep", NoteInput{Title: "K", Body: "k", Time: fixedTime}, "keep"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.SetNote(ctx, "docs/a", NoteInput{Title: "A", Body: "a0", Time: fixedTime}, "docs a"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.SetNote(ctx, "mine/x", NoteInput{Title: "X", Body: "x0", Time: fixedTime}, "mine x"); err != nil {
			t.Fatal(err)
		}
		s0, err := st.BranchGet(ctx, "default", "main")
		if err != nil {
			t.Fatal(err)
		}
		baseSnap, err := r.loadSnapshot(ctx, s0)
		if err != nil {
			t.Fatal(err)
		}
		baseTree, err := r.loadTree(ctx, baseSnap.Root)
		if err != nil {
			t.Fatal(err)
		}
		if oursDocs {
			if _, _, err := r.SetNote(ctx, "docs/a", NoteInput{Title: "A", Body: "a-ours", Time: fixedTime}, "docs a-ours"); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := r.SetNote(ctx, "mine/x", NoteInput{Title: "X", Body: "x1", Time: fixedTime}, "mine x1"); err != nil {
			t.Fatal(err)
		}
		oHead, err := st.BranchGet(ctx, "default", "main")
		if err != nil {
			t.Fatal(err)
		}
		oSnap, err := r.loadSnapshot(ctx, oHead)
		if err != nil {
			t.Fatal(err)
		}
		oursTree, err := r.loadTree(ctx, oSnap.Root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Reset(ctx, string(s0)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.SetNote(ctx, "docs/a", NoteInput{Title: "A", Body: "a-theirs", Time: fixedTime}, "docs a-theirs"); err != nil {
			t.Fatal(err)
		}
		tHead, err := st.BranchGet(ctx, "default", "main")
		if err != nil {
			t.Fatal(err)
		}
		tSnap, err := r.loadSnapshot(ctx, tHead)
		if err != nil {
			t.Fatal(err)
		}
		theirsTree, err := r.loadTree(ctx, tSnap.Root)
		if err != nil {
			t.Fatal(err)
		}
		return cs, r, baseTree, oursTree, theirsTree, oSnap.Root
	}
	// 剪枝场景:docs 只有 theirs 改(o==b → 整树取 theirs)、mine 只有 ours 改
	// (t==b → 整树取 ours)→ 根层地址判定直接收敛,tree 对象零读取
	cs, r, baseTree, oursTree, theirsTree, oursRoot := build(t, false)
	cs.treeGets = 0 // 构建期读取不计入
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, baseTree, oursTree, theirsTree, oursRoot, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	if cs.treeGets != 0 {
		t.Fatalf("Merkle 剪枝应零下钻(0 次 tree 读取),got %d", cs.treeGets)
	}
	if !node.dirty {
		t.Fatal("根层应有变更")
	}
	m := treeMap(node.tree)
	theirsDocs, _ := theirsTree.Lookup("docs")
	oursMine, _ := oursTree.Lookup("mine")
	if m["docs"].Type != object.EntryDir || m["docs"].Addr != theirsDocs.Addr {
		t.Fatalf("docs 应整树取 theirs(零下钻): %+v want %s", m["docs"], theirsDocs.Addr)
	}
	if m["mine"].Type != object.EntryDir || m["mine"].Addr != oursMine.Addr {
		t.Fatalf("mine 应整树取 ours(零下钻): %+v want %s", m["mine"], oursMine.Addr)
	}
	if len(out.conflicts) != 0 {
		t.Fatalf("剪枝场景不应有冲突: %v", out.conflicts)
	}
	// 对照:docs 双侧异变(b/o/t 三目录互异)→ 必须下钻,恰读三棵子树
	cs2, r2, baseTree2, oursTree2, theirsTree2, oursRoot2 := build(t, true)
	cs2.treeGets = 0
	out2 := &mergeOutput{}
	if _, err := r2.mergeTrees(ctx, baseTree2, oursTree2, theirsTree2, oursRoot2, nil, out2); err != nil {
		t.Fatal(err)
	}
	if cs2.treeGets != 3 {
		t.Fatalf("双侧目录异变应恰好下钻读取 3 棵子树,got %d", cs2.treeGets)
	}
	if len(out2.conflicts) != 1 || out2.conflicts[0].Path != "docs/a" || out2.conflicts[0].Kind != mergeKindContent {
		t.Fatalf("docs/a 双侧异改应为 content 冲突: %+v", out2.conflicts)
	}
}

// TestMergeFormConflictTypeCollision:形态对撞(一侧把条目改成目录,另一侧
// 修改条目本体)→ 响亮报 type 冲突,树中占位取 ours,不落库。
func TestMergeFormConflictTypeCollision(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	s0, _ := seedCommon(t, a, aSt, b)
	_ = s0
	// 专用基线:note go
	_, g0, err := a.SetNote(ctx, "go", NoteInput{Title: "Go", Body: "go v0", Time: fixedTime}, "go v0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	// ours:把 go 条目扩成目录 go/inner
	if _, err := a.RemoveNote(ctx, "go", "ours rm go"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SetNote(ctx, "go/inner", NoteInput{Title: "Inner", Body: "inner body", Time: fixedTime}, "ours go dir"); err != nil {
		t.Fatal(err)
	}
	oHead, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	// theirs:修改 go 条目本体
	_, g2, err := b.SetNote(ctx, "go", NoteInput{Title: "Go", Body: "go v2", Time: fixedTime}, "theirs go v2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bSt.BranchGet(ctx, "default", "main"); err != nil {
		t.Fatal(err)
	}
	// ours 树中 go 应为目录条目
	oSnap, err := a.loadSnapshot(ctx, oHead)
	if err != nil {
		t.Fatal(err)
	}
	oTree, err := a.loadTree(ctx, oSnap.Root)
	if err != nil {
		t.Fatal(err)
	}
	goEntry, _ := oTree.Lookup("go")
	if goEntry.Type != object.EntryDir {
		t.Fatalf("ours 的 go 应为目录: %+v", goEntry)
	}
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("形态对撞应响亮报冲突,got %v", err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("应恰 1 条冲突: %v", res.Conflicts)
	}
	c := res.Conflicts[0]
	if c.Path != "go" || c.Kind != mergeKindType ||
		c.Base != g0 || c.Ours != goEntry.Addr || c.Theirs != g2 {
		t.Fatalf("type 冲突四元组不符: %+v (g0=%s dir=%s g2=%s)", c, g0, goEntry.Addr, g2)
	}
	// 冲突即停:指针未动、无双亲快照落库
	head, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	if head != oHead {
		t.Fatalf("冲突不应推进分支指针: %s", head)
	}
	if n := countSnapshotsWithTwoParents(t, aSt); n != 0 {
		t.Fatalf("冲突不应产生正式提交: %d", n)
	}
}

// TestMergeModifyDeleteConflict:删改对撞(双向)→ modify-delete 冲突。
func TestMergeModifyDeleteConflict(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	_, p0, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p0", Time: fixedTime}, "p0")
	if err != nil {
		t.Fatal(err)
	}
	_, q0, err := a.SetNote(ctx, "q", NoteInput{Title: "Q", Body: "q0", Time: fixedTime}, "q0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	// ours:改 p、删 q;theirs:删 p、改 q(双向删改对撞)
	_, p1, err := a.SetNote(ctx, "p", NoteInput{Title: "P", Body: "p1", Time: fixedTime}, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RemoveNote(ctx, "q", "ours rm q"); err != nil {
		t.Fatal(err)
	}
	if _, err := aSt.BranchGet(ctx, "default", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RemoveNote(ctx, "p", "theirs rm p"); err != nil {
		t.Fatal(err)
	}
	_, q1, err := b.SetNote(ctx, "q", NoteInput{Title: "Q", Body: "q1", Time: fixedTime}, "q1")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("删改对撞应报冲突,got %v", err)
	}
	if len(res.Conflicts) != 2 {
		t.Fatalf("应恰 2 条冲突: %v", res.Conflicts)
	}
	cp, cq := res.Conflicts[0], res.Conflicts[1]
	if cp.Path != "p" || cp.Kind != mergeKindModifyDelete || cp.Base != p0 || cp.Ours != p1 || cp.Theirs != "" {
		t.Fatalf("p 应为 modify-delete(ours 改/theirs 删): %+v", cp)
	}
	if cq.Path != "q" || cq.Kind != mergeKindModifyDelete || cq.Base != q0 || cq.Ours != "" || cq.Theirs != q1 {
		t.Fatalf("q 应为 modify-delete(ours 删/theirs 改): %+v", cq)
	}
	if n := countSnapshotsWithTwoParents(t, aSt); n != 0 {
		t.Fatalf("冲突不应产生正式提交: %d", n)
	}
}

// TestMergeDesignWorkedExample:复刻调研 §3.2 逐步演算(六类别一网打尽):
// 自动合并 3 条(go 同变、kb 单侧增、daily 单侧删),冲突 1 条(inbox 双侧异改),
// 冲突即停:指针不动、无正式提交。
func TestMergeDesignWorkedExample(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, true)
	res, merr := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	var mc *ErrMergeConflicts
	if !errors.As(merr, &mc) {
		t.Fatalf("inbox 双侧异改应报冲突,got %v", merr)
	}
	if res.AutoMerged != 3 {
		t.Fatalf("自动合并应为 3 条(go/kb/daily),got %d", res.AutoMerged)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("应恰 1 条冲突: %v", res.Conflicts)
	}
	// theirs 侧 inbox 地址
	theirsRef, err := sc.b.Note(ctx, "workflow/inbox")
	if err != nil {
		t.Fatal(err)
	}
	c := res.Conflicts[0]
	if c.Path != "workflow/inbox" || c.Kind != mergeKindContent ||
		c.Base != sc.baseNoteAddr["workflow/inbox"] || c.Ours != sc.inboxOurs || c.Theirs != theirsRef.Addr {
		t.Fatalf("冲突四元组不符: %+v (base=%s ours=%s theirs=%s)",
			c, sc.baseNoteAddr["workflow/inbox"], sc.inboxOurs, theirsRef.Addr)
	}
	if !strings.Contains(merr.Error(), "workflow/inbox") {
		t.Fatalf("冲突文案应含路径: %s", merr.Error())
	}
	// 冲突即停:指针不动、无双亲快照
	head, err := sc.aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	if head != sc.oursHead {
		t.Fatalf("冲突不应推进分支指针: %s", head)
	}
	if n := countSnapshotsWithTwoParents(t, sc.aSt); n != 0 {
		t.Fatalf("冲突不应产生正式提交: %d", n)
	}
}

// TestMergeZeroConflictTwoParents:零冲突一步合并——落双亲快照、双方内容齐备、
// 正式提交建索引。
func TestMergeZeroConflictTwoParents(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatalf("零冲突合并应成功: %v", err)
	}
	if res.UpToDate || res.FastForward {
		t.Fatal("分叉合并不应标记 up-to-date/ff")
	}
	if res.Snap == "" || res.Root == "" {
		t.Fatal("零冲突应落合并快照与合并树")
	}
	if res.AutoMerged != 3 {
		t.Fatalf("自动合并应为 3 条(go/kb+workflow 整树),got %d", res.AutoMerged)
	}
	if got := parentsSet(t, sc.a, res.Snap); !got[sc.oursHead] || !got[sc.theirsHead] || len(got) != 2 {
		t.Fatalf("合并快照 Parents 应为 [ours, theirs]: %v", got)
	}
	snap, err := sc.a.loadSnapshot(ctx, res.Snap)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index == "" {
		t.Fatal("正式合并提交必须建索引(与一切正式提交同纪律)")
	}
	// 双方内容齐备:ours 新增可读、theirs 删除生效、未动条目保持基线
	if _, err := sc.a.Note(ctx, "kb"); err != nil {
		t.Fatalf("ours 新增的 kb 应可读: %v", err)
	}
	if _, err := sc.a.Note(ctx, "workflow/daily"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("theirs 删除的 daily 不应可见: %v", err)
	}
	ref, err := sc.a.Note(ctx, "workflow/inbox")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Addr != sc.baseNoteAddr["workflow/inbox"] {
		t.Fatalf("双方未动的 inbox 应保持基线地址: %s", ref.Addr)
	}
	goref, err := sc.a.Note(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goref.Body), "channel 关闭后 range 退出") {
		t.Fatalf("双侧同变的 go 应为合并后内容: %s", goref.Body)
	}
}

// TestMergeZeroTreeDiffStillCommits:theirs 变更已被 ours 全包含(零树差异)
// 仍要落合并快照——价值在拓扑(两链可达),不在树;索引结构共享复用。
func TestMergeZeroTreeDiffStillCommits(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	s0, _ := seedCommon(t, a, aSt, b)
	_ = s0
	input := NoteInput{Title: "X", Body: "same content", Time: fixedTime}
	o1, _, err := a.SetNote(ctx, "x", input, "ours x")
	if err != nil {
		t.Fatal(err)
	}
	t1, _, err := b.SetNote(ctx, "x", input, "theirs x")
	if err != nil {
		t.Fatal(err)
	}
	oursSnap, err := a.loadSnapshot(ctx, o1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatalf("合并应成功: %v", err)
	}
	if res.Snap == "" {
		t.Fatal("零树差异仍应落合并快照,否则 theirs 提交不可达、下次 GC 被清扫")
	}
	snap, err := a.loadSnapshot(ctx, res.Snap)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Root != oursSnap.Root {
		t.Fatalf("合并树应与 ours 树同地址(结构共享): %s vs %s", snap.Root, oursSnap.Root)
	}
	if snap.Index != oursSnap.Index {
		t.Fatalf("零差异索引应复用 ours 索引地址: %s vs %s", snap.Index, oursSnap.Index)
	}
	if got := parentsSet(t, a, res.Snap); !got[o1] || !got[t1] || len(got) != 2 {
		t.Fatalf("合并快照 Parents 应为 [o1, t1]: %v", got)
	}
	if res.AutoMerged != 1 {
		t.Fatalf("x 双侧同变应计 1,got %d", res.AutoMerged)
	}
}

// TestMergeTwoParentFSCKAndReachability:双亲快照 fsck 通过,两条 parent 链
// 都可达(theirs 侧历史并入短标识可达集)。
func TestMergeTwoParentFSCKAndReachability(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := sc.a.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Problems) != 0 {
		t.Fatalf("合并后 fsck 应零问题: %v", fs.Problems)
	}
	// theirs 头与 theirs 链上快照:沿全部 parents 可达,短标识可解析
	if got, err := sc.a.Resolve(ctx, string(sc.theirsHead)[:16]); err != nil || got != sc.theirsHead {
		t.Fatalf("theirs 头应进入可达集: %s %v", got, err)
	}
	if _, err := sc.a.NoteAt(ctx, "workflow/inbox", string(sc.theirsHead)[:16]); err != nil {
		t.Fatalf("theirs 链上的历史条目应可 --at 读取: %v", err)
	}
	logs, err := sc.a.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || logs[0].Addr != res.Snap {
		t.Fatalf("log 首条应为合并快照: %v", logs)
	}
}

// TestMergeGCKeepsBothChains:GC 清扫后两条 parent 链与合并结果都保留;
// gc --keep-last 1 下合并快照本体与索引保留(深度按最浅计)。
func TestMergeGCKeepsBothChains(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ours, theirs, snap := sc.oursHead, sc.theirsHead, res.Snap
	gc1, err := sc.a.GC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = gc1
	for _, addr := range []hash.Address{ours, theirs, snap} {
		ok, err := sc.aSt.Has(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("GC 后合并历史对象不应被清扫: %s", addr)
		}
	}
	if _, err := sc.a.Note(ctx, "kb"); err != nil {
		t.Fatalf("ours 侧内容应保留: %v", err)
	}
	if _, err := sc.a.NoteAt(ctx, "workflow/inbox", string(theirs)[:16]); err != nil {
		t.Fatalf("theirs 链内容应保留: %v", err)
	}
	// --keep-last 1:深度 ≥1 的历史快照索引被精简,合并快照本体与索引保留
	gc2, err := sc.a.GCWithKeepLast(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gc2.Swept == 0 {
		t.Fatal("keep-last 1 应精简历史索引(清扫数应大于 0)")
	}
	snapObj, err := sc.a.loadSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []hash.Address{ours, theirs, snap, snapObj.Index} {
		ok, err := sc.aSt.Has(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("keep-last 下合并快照本体与索引不应被清扫: %s", addr)
		}
	}
	fs, err := sc.a.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Problems) != 0 {
		t.Fatalf("keep-last 精简后 fsck 应按水位豁免、零问题: %v", fs.Problems)
	}
}

// TestMergeBackupRestoreRoundtrip:backup/restore 往返,双亲快照逐字段还原。
func TestMergeBackupRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := DumpLibrary(ctx, sc.aSt, &buf); err != nil {
		t.Fatal(err)
	}
	fst, _ := freshStore(t)
	if _, err := RestoreLibrary(ctx, fst, &buf, false); err != nil {
		t.Fatal(err)
	}
	// 双亲快照逐字段还原
	want, err := sc.a.loadSnapshot(ctx, res.Snap)
	if err != nil {
		t.Fatal(err)
	}
	data, kind, err := fst.Get(ctx, res.Snap)
	if err != nil {
		t.Fatalf("恢复库应含合并快照: %v", err)
	}
	if kind != object.KindSnapshot {
		t.Fatalf("地址 %s 应为 snapshot,got %s", res.Snap, kind)
	}
	got, err := object.DecodeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != want.Root || got.Time != want.Time || got.Message != want.Message || got.Index != want.Index {
		t.Fatalf("快照字段还原不符: got %+v want %+v", got, want)
	}
	if len(got.Parents) != 2 {
		t.Fatalf("恢复后应保留双亲: %v", got.Parents)
	}
	parents := map[hash.Address]bool{}
	for _, p := range got.Parents {
		parents[p] = true
	}
	if !parents[sc.oursHead] || !parents[sc.theirsHead] {
		t.Fatalf("恢复后双亲应为 ours/theirs 头: %v", got.Parents)
	}
	if head, err := fst.BranchGet(ctx, "default", "main"); err != nil || head != res.Snap {
		t.Fatalf("恢复后分支指针应为合并快照: %s %v", head, err)
	}
	// 恢复库 fsck 复核 + 双侧内容可读
	fr := Open(fst, Config{Branch: "main", Now: func() int64 { return fixedTime }})
	fs, err := fr.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Problems) != 0 {
		t.Fatalf("恢复后 fsck 应零问题: %v", fs.Problems)
	}
	if _, err := fr.Note(ctx, "kb"); err != nil {
		t.Fatalf("恢复后 ours 侧内容应可读: %v", err)
	}
	if _, err := fr.Note(ctx, "go"); err != nil {
		t.Fatalf("恢复后合并内容应可读: %v", err)
	}
}

// TestMergePullTransfersOnlyMissing:pull 对合并历史只传缺失对象。
func TestMergePullTransfersOnlyMissing(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// B(theirs)回拉 A 的合并历史:theirs 头 ∈ ancestors(合并快照)→ fast-forward
	objectSet := func(s store.Store) map[hash.Address]bool {
		m := map[hash.Address]bool{}
		if err := s.List(ctx, func(info store.ObjectInfo) error { m[info.Addr] = true; return nil }); err != nil {
			t.Fatal(err)
		}
		return m
	}
	before := objectSet(sc.bSt)
	res2, err := sc.b.Pull(ctx, sc.aSt, "default", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.FastForward || res2.Transferred == 0 {
		t.Fatalf("应 fast-forward 且有增量传输: %+v", res2)
	}
	after := objectSet(sc.bSt)
	if len(after)-len(before) != res2.Transferred {
		t.Fatalf("传输数应恰等于新增对象数(只传缺失): +%d vs %d", len(after)-len(before), res2.Transferred)
	}
	if ok, err := sc.bSt.Has(ctx, sc.oursHead); err != nil || !ok {
		t.Fatalf("拉取后 ours 链应可达: %v %v", ok, err)
	}
	if _, err := sc.b.Note(ctx, "kb"); err != nil {
		t.Fatalf("拉取后 B 应可读 ours 侧新增: %v", err)
	}
	_ = res
	// 再拉:已是最新,零传输
	res3, err := sc.b.Pull(ctx, sc.aSt, "default", "main", false)
	if err != nil || !res3.UpToDate || res3.Transferred != 0 {
		t.Fatalf("二次拉取应 up-to-date 零传输: %+v %v", res3, err)
	}
}

// TestMergeResetToPreMergeSides:双亲历史上 reset 语义正确——回退到合并前
// 任一侧都可用(含 Parents[1] 侧);放弃的合并提交交 GC。
func TestMergeResetToPreMergeSides(t *testing.T) {
	ctx := context.Background()
	sc := runMergeScenario(t, fixedTime, false)
	res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Snap
	// 回退到 ours 侧(first-parent):放弃集 = {合并快照, theirs 侧 go 提交,
	// theirs 侧 daily 删除}(两侧 go 提交消息不同 → 不同快照地址,同根树)
	r1, err := sc.a.Reset(ctx, string(sc.oursHead)[:16])
	if err != nil {
		t.Fatalf("回退到 ours 侧应可用: %v", err)
	}
	if r1.To != sc.oursHead || r1.Abandoned != 3 {
		t.Fatalf("回退结果不符: %+v", r1)
	}
	if _, err := sc.a.Note(ctx, "workflow/daily"); err != nil {
		t.Fatalf("放弃合并后 theirs 删除应一并回退: %v", err)
	}
	logs, err := sc.a.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || logs[0].Addr != sc.oursHead {
		t.Fatalf("回退后头应为 ours 头: %v", logs)
	}
	// 重新合并(同输入同时间 → 对象幂等同地址),theirs 删除重新生效
	res2, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
	if err != nil {
		t.Fatalf("重新合并应成功: %v", err)
	}
	if res2.Snap != m {
		t.Fatalf("同输入重复合并应幂等落同一快照地址: %s vs %s", res2.Snap, m)
	}
	if _, err := sc.a.Note(ctx, "workflow/daily"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("重新合并后 daily 删除应生效: %v", err)
	}
	// 回退到 theirs 侧(Parents[1]):DAG 感知,同样可用
	r2, err := sc.a.Reset(ctx, string(sc.theirsHead)[:16])
	if err != nil {
		t.Fatalf("回退到 theirs 侧应可用(双亲历史): %v", err)
	}
	if r2.To != sc.theirsHead || r2.Abandoned != 3 {
		t.Fatalf("theirs 侧回退结果不符: %+v", r2)
	}
	if _, err := sc.a.Note(ctx, "kb"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("theirs 侧视图不应含 ours 侧新增: %v", err)
	}
	logs2, err := sc.a.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs2) == 0 || logs2[0].Addr != sc.theirsHead {
		t.Fatalf("回退后头应为 theirs 头: %v", logs2)
	}
	_ = m
}

// TestMergePullLocalAheadUpToDate:本地领先(远端头 ∈ 本地祖先链)时,
// 无旗标 pull 为「已是最新」空操作,不再要求 --force;--force 仍保持覆盖回退。
func TestMergePullLocalAheadUpToDate(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	s0, _ := seedCommon(t, a, aSt, b)
	o1, _, err := a.SetNote(ctx, "a1", NoteInput{Title: "A1", Body: "a1", Time: fixedTime}, "a1")
	if err != nil {
		t.Fatal(err)
	}
	// 本地领先:远端头 s0 ∈ ancestors(o1) → 已是最新空操作
	res, err := a.Pull(ctx, bSt, "default", "main", false)
	if err != nil {
		t.Fatalf("本地领先不应再报分叉: %v", err)
	}
	if !res.UpToDate || res.Transferred != 0 {
		t.Fatalf("本地领先应为零传输空操作: %+v", res)
	}
	logs, err := a.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || logs[0].Addr != o1 {
		t.Fatalf("空操作不应移动指针: %v", logs)
	}
	// --force 保持覆盖(回退)语义:指针回拨到远端头
	res2, err := a.Pull(ctx, bSt, "default", "main", true)
	if err != nil {
		t.Fatalf("force pull 应成功: %v", err)
	}
	if res2.UpToDate {
		t.Fatal("force 覆盖不应标记 up-to-date")
	}
	logs2, err := a.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs2) == 0 || logs2[0].Addr != s0 {
		t.Fatalf("force 应回拨到远端头: %v", logs2)
	}
	if _, err := a.Note(ctx, "a1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("force 覆盖后本地独有提交应退出当前视图: %v", err)
	}
}

// TestMergeDeterministicAcrossTime:同一内容注入不同时间源,合并树地址
// 逐字节一致(确定性;快照地址随时间变化恰好证明时间确实被注入)。
func TestMergeDeterministicAcrossTime(t *testing.T) {
	ctx := context.Background()
	mergeAt := func(now int64) (root, snap hash.Address) {
		sc := runMergeScenario(t, now, false)
		res, err := sc.a.Merge(ctx, sc.bSt, "default", "main", MergeOptions{})
		if err != nil {
			t.Fatalf("合并应成功: %v", err)
		}
		return res.Root, res.Snap
	}
	root1, snap1 := mergeAt(fixedTime)
	root2, snap2 := mergeAt(fixedTime + 999)
	if root1 == "" || root1 != root2 {
		t.Fatalf("合并树地址应与时间源无关: %s vs %s", root1, root2)
	}
	if snap1 == snap2 {
		t.Fatal("不同时间源应产生不同快照地址(证明时间确实被注入)")
	}
}

// TestMergeConflictListComplete:冲突清单完整性——跨层级、跨类别的全部冲突
// 一次报齐(路径/类别/三侧地址逐项核对),且不落库。
func TestMergeConflictListComplete(t *testing.T) {
	ctx := context.Background()
	a, b, aSt, bSt := mergePair(t, fixedTime)
	// 基线:x(note)、d/a(note)、go(note)
	_, x0, err := a.SetNote(ctx, "x", NoteInput{Title: "X", Body: "x0", Time: fixedTime}, "x0")
	if err != nil {
		t.Fatal(err)
	}
	_, a0, err := a.SetNote(ctx, "d/a", NoteInput{Title: "A", Body: "a0", Time: fixedTime}, "a0")
	if err != nil {
		t.Fatal(err)
	}
	_, g0, err := a.SetNote(ctx, "go", NoteInput{Title: "Go", Body: "g0", Time: fixedTime}, "g0")
	if err != nil {
		t.Fatal(err)
	}
	s0, _, err := a.SetNote(ctx, "seed", NoteInput{Title: "Seed", Body: "seed", Time: fixedTime}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	_ = s0
	if _, err := b.Pull(ctx, aSt, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	// ours:x 异改(content)、d/a 修改、go 改成目录(形态)
	_, x1, err := a.SetNote(ctx, "x", NoteInput{Title: "X", Body: "x1", Time: fixedTime}, "x1")
	if err != nil {
		t.Fatal(err)
	}
	_, a1, err := a.SetNote(ctx, "d/a", NoteInput{Title: "A", Body: "a1", Time: fixedTime}, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RemoveNote(ctx, "go", "ours rm go"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SetNote(ctx, "go/inner", NoteInput{Title: "Inner", Body: "inner", Time: fixedTime}, "ours go dir"); err != nil {
		t.Fatal(err)
	}
	oHead, err := aSt.BranchGet(ctx, "default", "main")
	if err != nil {
		t.Fatal(err)
	}
	oSnap, err := a.loadSnapshot(ctx, oHead)
	if err != nil {
		t.Fatal(err)
	}
	oTree, err := a.loadTree(ctx, oSnap.Root)
	if err != nil {
		t.Fatal(err)
	}
	goDir, _ := oTree.Lookup("go")
	if goDir.Type != object.EntryDir {
		t.Fatalf("ours 的 go 应为目录: %+v", goDir)
	}
	// theirs:x 异改、d/a 删除、go 条目本体修改
	_, x2, err := b.SetNote(ctx, "x", NoteInput{Title: "X", Body: "x2", Time: fixedTime}, "x2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.RemoveNote(ctx, "d/a", "theirs rm d/a"); err != nil {
		t.Fatal(err)
	}
	_, g2, err := b.SetNote(ctx, "go", NoteInput{Title: "Go", Body: "g2", Time: fixedTime}, "g2")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Merge(ctx, bSt, "default", "main", MergeOptions{})
	var mc *ErrMergeConflicts
	if !errors.As(err, &mc) {
		t.Fatalf("应报冲突: %v", err)
	}
	if len(res.Conflicts) != 3 {
		t.Fatalf("应报齐 3 条冲突: %v", res.Conflicts)
	}
	// 清单按路径字典序:d/a < go < x;类别与三侧地址逐项核对
	ca, cg, cx := res.Conflicts[0], res.Conflicts[1], res.Conflicts[2]
	if ca.Path != "d/a" || ca.Kind != mergeKindModifyDelete || ca.Base != a0 || ca.Ours != a1 || ca.Theirs != "" {
		t.Fatalf("d/a 冲突不符: %+v", ca)
	}
	if cg.Path != "go" || cg.Kind != mergeKindType || cg.Base != g0 || cg.Ours != goDir.Addr || cg.Theirs != g2 {
		t.Fatalf("go 冲突不符: %+v", cg)
	}
	if cx.Path != "x" || cx.Kind != mergeKindContent || cx.Base != x0 || cx.Ours != x1 || cx.Theirs != x2 {
		t.Fatalf("x 冲突不符: %+v", cx)
	}
	if n := countSnapshotsWithTwoParents(t, aSt); n != 0 {
		t.Fatalf("冲突不应产生正式提交: %d", n)
	}
	if head, err := aSt.BranchGet(ctx, "default", "main"); err != nil || head != oHead {
		t.Fatalf("冲突不应推进分支指针: %s %v", head, err)
	}
}
