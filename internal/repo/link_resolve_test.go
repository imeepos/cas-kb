package repo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

func TestResolveLink(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_link")

	set := func(path, title string) (noteAddr string) {
		t.Helper()
		_, addr, err := r.SetNote(ctx, path, NoteInput{Title: title, Body: "b of " + title}, "add "+path)
		if err != nil {
			t.Fatal(err)
		}
		return string(addr)
	}
	set("go/channel", "Channel")
	set("go/adv/x", "X in adv")
	set("web/x", "X in web")
	taskAddr := set("task", "Task")

	// 1) 全路径精确匹配
	ref, err := r.ResolveLink(ctx, "go/channel")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != "go/channel" || ref.Note.Meta.Title != "Channel" {
		t.Fatalf("全路径解析不符: %+v", ref)
	}

	// 2) 叶子名全库唯一回退
	ref, err = r.ResolveLink(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != "task" || string(ref.Addr) != taskAddr {
		t.Fatalf("叶名唯一回退不符: %+v", ref)
	}

	// 3) 歧义:两个 x,列出候选且按路径字典序
	_, err = r.ResolveLink(ctx, "x")
	if !errors.Is(err, ErrAmbiguousSlug) {
		t.Fatalf("应报歧义错误: %v", err)
	}
	if !strings.Contains(err.Error(), "go/adv/x, web/x") {
		t.Fatalf("歧义应列出候选: %v", err)
	}

	// 4) 无解析目标
	if _, err := r.ResolveLink(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("应报 NotFound: %v", err)
	}

	// 5) 命中目录报错
	if _, err := r.ResolveLink(ctx, "go"); err == nil || !strings.Contains(err.Error(), "命中目录") {
		t.Fatalf("链接命中目录应报错: %v", err)
	}
}

func TestResolveLinkAtVersionSelfConsistent(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m4_link_at")

	c1, addr1, err := r.SetNote(ctx, "a", NoteInput{Title: "A v1", Body: "old"}, "add a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Body: "b"}, "add b"); err != nil {
		t.Fatal(err)
	}
	c2, addr2, err := r.SetNote(ctx, "a", NoteInput{Title: "A v2", Body: "new"}, "modify a")
	if err != nil {
		t.Fatal(err)
	}

	// 旧快照解析到旧 A,当前解析到新 A
	oldRef, err := r.ResolveLinkAt(ctx, "a", string(c1))
	if err != nil {
		t.Fatal(err)
	}
	if string(oldRef.Addr) != string(addr1) {
		t.Fatalf("旧快照应解析到旧 A: %s != %s", oldRef.Addr, addr1)
	}
	curRef, err := r.ResolveLink(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(curRef.Addr) != string(addr2) {
		t.Fatalf("当前应解析到新 A: %s != %s", curRef.Addr, addr2)
	}
	if _, err := r.ResolveLinkAt(ctx, "a", string(c2)); err != nil {
		t.Fatal(err)
	}

	// c1 时刻 b 不存在:叶名回退在该快照应报 NotFound
	if _, err := r.ResolveLinkAt(ctx, "b", string(c1)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("历史快照内 b 应不存在: %v", err)
	}
}
