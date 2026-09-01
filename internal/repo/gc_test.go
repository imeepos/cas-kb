package repo

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
)

func TestM3_GCDeletesOnlyUnreachable(t *testing.T) {
	ctx := context.Background()
	r, s, _ := newRepo(t, "m3_gc")
	orphanAddr, err := s.Put(ctx, object.KindBlob, []byte("orphan object body"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "keep a body"}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Body: "keep b body"}, "b"); err != nil {
		t.Fatal(err)
	}
	if has, _ := s.Has(ctx, orphanAddr); !has {
		t.Fatal("孤儿对象应已写入")
	}
	res, err := r.GC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Swept == 0 {
		t.Fatal("GC 应清扫至少一个不可达对象")
	}
	if has, _ := s.Has(ctx, orphanAddr); has {
		t.Fatal("孤儿对象应被 GC 删除")
	}
	if _, err := r.Note(ctx, "a"); err != nil {
		t.Fatalf("分支可达对象不应被删: %v", err)
	}
	fres, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fres.Problems) > 0 {
		t.Fatalf("GC 后 fsck 不应有问题: %v", fres.Problems)
	}
}
