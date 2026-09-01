package repo

import (
	"context"
	"testing"
)

func TestM2_CommitCreatesSnapshot(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "m2_commit")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add a"); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Commit(ctx, "checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := r.Log(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("Commit 后应有两个快照,got %d", len(logs))
	}
	if logs[0].Addr != snap || logs[0].Snapshot.Message != "checkpoint" {
		t.Fatalf("最新快照应为 Commit 产物: %v", logs[0])
	}
}
