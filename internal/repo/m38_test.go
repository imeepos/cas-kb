package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// M3.8 目录层级:嵌套路径条目 roundtrip 与各层级递归列出。
func TestM38_NestedNoteRoundtrip(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_nested")
	_, addr, err := r.SetNote(ctx, "go/concurrency/channel", NoteInput{Title: "Channel", Body: "chan 语义"}, "add nested")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Note(ctx, "go/concurrency/channel")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Note.Meta.Title != "Channel" || string(ref.Body) != "chan 语义" || ref.Addr != addr {
		t.Fatalf("嵌套条目 roundtrip 不一致: %+v", ref)
	}
	if ref.Path != "go/concurrency/channel" || ref.Slug != "channel" {
		t.Fatalf("Path/Slug 应为全路径/叶段: %q %q", ref.Path, ref.Slug)
	}
	all, err := r.ListNotes(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("根目录递归列出应含 1 条: %v %v", all, err)
	}
	inGo, err := r.ListNotes(ctx, "go")
	if err != nil || len(inGo) != 1 || inGo[0].Path != "go/concurrency/channel" {
		t.Fatalf("go 目录递归列出失败: %v %v", inGo, err)
	}
	ref2, err := r.NoteAt(ctx, "go/concurrency/channel", r.Branch())
	if err != nil || ref2.Addr != addr {
		t.Fatalf("NoteAt 按路径读取失败: %v %v", ref2, err)
	}
}

// M3.8:Mkdir 幂等、DirLs 目录在前排序。
func TestM38_MkdirAndDirLs(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_mkdir")
	s1, created, err := r.Mkdir(ctx, "a/b", "mkdir a/b")
	if err != nil || !created || s1 == "" {
		t.Fatalf("首次建目录应产生快照: %v %v %v", s1, created, err)
	}
	_, created2, err := r.Mkdir(ctx, "a/b", "mkdir again")
	if err != nil || created2 {
		t.Fatalf("重复建目录应幂等: %v %v", created2, err)
	}
	root, err := r.DirLs(ctx, "")
	if err != nil || len(root) != 1 || root[0].Name != "a" || root[0].Type != object.EntryDir {
		t.Fatalf("根目录应含目录 a: %+v %v", root, err)
	}
	inA, err := r.DirLs(ctx, "a")
	if err != nil || len(inA) != 1 || inA[0].Name != "b" || inA[0].Type != object.EntryDir {
		t.Fatalf("a 应含目录 b: %+v %v", inA, err)
	}
	if _, err := r.DirLs(ctx, "a/b"); err != nil {
		t.Fatalf("空目录应可读: %v", err)
	}
}

// M3.8:路径冲突响亮失败(中间段是条目、叶子是目录、非法路径)。
func TestM38_PathConflicts(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_conflict")
	if _, _, err := r.SetNote(ctx, "x/y", NoteInput{Title: "X"}, "add x/y"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "x/y/z", NoteInput{Title: "Z"}, "through note"); err == nil {
		t.Fatal("中间段是条目时应拒绝")
	}
	if _, err := r.Note(ctx, "x/y/z"); err == nil {
		t.Fatal("穿过条目读条目应拒绝")
	}
	if _, _, err := r.Mkdir(ctx, "d", "mkdir d"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "d", NoteInput{Title: "DD"}, "leaf is dir"); err == nil {
		t.Fatal("目标路径已是目录时应拒绝写入条目")
	}
	if _, err := r.Note(ctx, "d"); err == nil {
		t.Fatal("按条目读取目录应拒绝")
	}
	for _, bad := range []string{"a//b", "/a", "a/", ".", "..", "a/./b", "a/../b"} {
		if _, _, err := r.SetNote(ctx, bad, NoteInput{Title: "B"}, "bad path"); err == nil {
			t.Fatalf("非法路径 %q 应拒绝", bad)
		}
	}
	if _, _, err := r.Mkdir(ctx, "", "mkdir root"); err == nil {
		t.Fatal("空目录路径应拒绝(根目录天然存在)")
	}
}

// M3.8:目录删除语义——非空拒绝、--force 递归、旧快照时间旅行不变。
func TestM38_RemoveDirAndTimeTravel(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_rmdir")
	if _, _, err := r.SetNote(ctx, "d/n1", NoteInput{Title: "N1", Body: "b1"}, "add n1"); err != nil {
		t.Fatal(err)
	}
	sBefore, _, err := r.SetNote(ctx, "d/sub/n2", NoteInput{Title: "N2"}, "add n2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveDir(ctx, "d", "rm d", false); err == nil {
		t.Fatal("非空目录非递归删除应拒绝")
	}
	if _, err := r.RemoveDir(ctx, "d", "rm d", true); err != nil {
		t.Fatalf("递归删除应成功: %v", err)
	}
	if _, err := r.Note(ctx, "d/n1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("删除后条目应不存在: %v", err)
	}
	if _, err := r.DirLs(ctx, "d"); err == nil {
		t.Fatal("删除后目录应不存在")
	}
	// 旧快照时间旅行:条目与目录仍在
	ref, err := r.NoteAt(ctx, "d/n1", string(sBefore))
	if err != nil || ref.Note.Meta.Title != "N1" {
		t.Fatalf("旧快照应仍可读 d/n1: %v %v", ref, err)
	}
}

// M3.8:删除条目后目录保留(目录是实体,不同于 git)。
func TestM38_EmptyDirSurvivesNoteRemoval(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_emptydir")
	if _, _, err := r.SetNote(ctx, "d/n", NoteInput{Title: "N"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveNote(ctx, "d/n", "rm n"); err != nil {
		t.Fatal(err)
	}
	inD, err := r.DirLs(ctx, "d")
	if err != nil || len(inD) != 0 {
		t.Fatalf("目录 d 应保留为空目录: %+v %v", inD, err)
	}
	root, err := r.DirLs(ctx, "")
	if err != nil || len(root) != 1 || root[0].Name != "d" {
		t.Fatalf("根目录应仍含 d: %+v %v", root, err)
	}
}

// M3.8:diff 按全路径比较;目录间移动 = 旧路径 removed + 新路径 added(地址不变)。
func TestM38_DiffAndMoveAcrossDirs(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_diff")
	_, addrM, err := r.SetNote(ctx, "m", NoteInput{Title: "M v1"}, "add m")
	if err != nil {
		t.Fatal(err)
	}
	c1, _, err := r.SetNote(ctx, "g/n", NoteInput{Title: "N v1"}, "add g/n")
	if err != nil {
		t.Fatal(err)
	}
	_, addrN2, err := r.SetNote(ctx, "g/n", NoteInput{Title: "N v2"}, "modify g/n")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := r.Diff(ctx, string(c1), r.Branch())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "g/n" || changes[0].Type != ChangeUpdated {
		t.Fatalf("应只有 g/n updated: %+v", changes)
	}
	if _, _, err := r.SetNote(ctx, "moved", NoteInput{Title: "M v1"}, "copy to moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveNote(ctx, "m", "remove m"); err != nil {
		t.Fatal(err)
	}
	head, err := r.Resolve(ctx, r.Branch())
	if err != nil {
		t.Fatal(err)
	}
	changes3, err := r.Diff(ctx, string(c1), string(head))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Change{}
	for _, ch := range changes3 {
		byPath[ch.Path] = ch
	}
	if byPath["m"].Type != ChangeRemoved || byPath["moved"].Type != ChangeAdded {
		t.Fatalf("移动应表现为旧路径 removed + 新路径 added: %+v", changes3)
	}
	if byPath["moved"].To != addrM {
		t.Fatal("移动后地址应不变(内容寻址)")
	}
	if byPath["g/n"].To != addrN2 {
		t.Fatal("g/n 应指向新版本")
	}
}

// M3.8:DirTree 层级视图(dir/note 结构与标题)。
func TestM38_DirTree(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m38_tree")
	if _, _, err := r.SetNote(ctx, "go/syntax", NoteInput{Title: "语法"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "go/conc/chan", NoteInput{Title: "Channel"}, "add"); err != nil {
		t.Fatal(err)
	}
	root, err := r.DirTree(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 || root.Children[0].Name != "go" {
		t.Fatalf("根下应只有 go: %+v", root.Children)
	}
	goDir := root.Children[0]
	if len(goDir.Children) != 2 || goDir.Children[0].Name != "conc" || goDir.Children[1].Name != "syntax" {
		t.Fatalf("go 下应为 conc + syntax: %+v", goDir.Children)
	}
	syn := goDir.Children[1]
	if syn.Type != object.EntryNote || syn.Title != "语法" {
		t.Fatalf("syntax 节点应带标题: %+v", syn)
	}
	sub, err := r.DirTree(ctx, "go/conc")
	if err != nil || len(sub.Children) != 1 || sub.Children[0].Title != "Channel" {
		t.Fatalf("子树视图失败: %+v %v", sub, err)
	}
}

// M3.8:force 删目录 + reset 放弃历史 + GC 清扫,fsck 通过。
func TestM38_GCAfterForceRemoveAndReset(t *testing.T) {
	ctx := context.Background()
	r, s, dsn := newRepo(t, "m38_gc")
	if _, _, err := r.Mkdir(ctx, "d", "mkdir d"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "d/n", NoteInput{Title: "N", Body: "body"}, "add d/n"); err != nil {
		t.Fatal(err)
	}
	logEntries, err := r.Log(ctx)
	if err != nil || len(logEntries) != 2 {
		t.Fatalf("此时应有 2 个快照: %v %v", logEntries, err)
	}
	// Log 最新在前;回退到最老快照(mkdir d),放弃其后的全部历史
	oldest := logEntries[len(logEntries)-1].Addr
	if _, err := r.RemoveDir(ctx, "d", "rm d", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reset(ctx, string(oldest)); err != nil {
		t.Fatalf("回退到最老快照: %v", err)
	}
	res, err := r.GC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Swept == 0 {
		t.Fatal("放弃历史后 GC 应清扫被删子树对象")
	}
	fs, err := r.FSCK(ctx)
	if err != nil || len(fs.Problems) != 0 {
		t.Fatalf("GC 后 fsck 应通过: %+v %v", fs.Problems, err)
	}
	_ = s
	_ = dsn
}
