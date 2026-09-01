package repo

import (
	"context"
	"testing"
)

func TestM3_FSCKDetectsTamper(t *testing.T) {
	ctx := context.Background()
	r, _, dsn := newRepo(t, "m3_fsck_tamper")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "body a"}, "a"); err != nil {
		t.Fatal(err)
	}
	ref, err := r.Note(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	execDB(t, dsn, "UPDATE objects SET data = $2 WHERE addr = $1", string(ref.Note.Body), []byte("tampered!"))
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) == 0 {
		t.Fatal("篡改字节应被发现")
	}
}

func TestM3_FSCKDetectsMissingRef(t *testing.T) {
	ctx := context.Background()
	r, _, dsn := newRepo(t, "m3_fsck_ref")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "body a"}, "a"); err != nil {
		t.Fatal(err)
	}
	ref, err := r.Note(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	execDB(t, dsn, "DELETE FROM objects WHERE addr = $1", string(ref.Note.Body))
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) == 0 {
		t.Fatal("缺失引用应被发现")
	}
}
