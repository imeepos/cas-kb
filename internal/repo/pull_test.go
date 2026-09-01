package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

// seedRemoteFastForward 建远端 r1/r2 两条笔记,返回本地与远端仓库。
func seedRemoteFastForward(t *testing.T) (*Repo, store.Store, *Repo) {
	ctx := context.Background()
	local, _, _ := newRepo(t, "m3_pull_ff")
	remote := openRemote(t)
	remoteRepo := Open(remote, Config{Branch: "main", Now: func() int64 { return fixedTime }})
	if _, _, err := remoteRepo.SetNote(ctx, "r1", NoteInput{Title: "R1", Body: "r1 unique body"}, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := remoteRepo.SetNote(ctx, "r2", NoteInput{Title: "R2", Body: "r2 unique body"}, "r2"); err != nil {
		t.Fatal(err)
	}
	return local, remote, remoteRepo
}

func TestM3_PullFastForward(t *testing.T) {
	ctx := context.Background()
	local, remote, _ := seedRemoteFastForward(t)
	res, err := local.Pull(ctx, remote, "default", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FastForward {
		t.Fatal("空库 pull 应 fast-forward")
	}
	// M4 起快照携带检索索引(DESIGN §7):8 个业务对象
	// + 两次提交各 1 个索引根 + r1 三桶分片 + r2 重写三桶分片(r1/unique/body、r2/unique/body)
	if res.Transferred != 16 {
		t.Fatalf("首次应传输 16 个对象,got %d", res.Transferred)
	}
	if _, err := local.Note(ctx, "r1"); err != nil {
		t.Fatalf("pull 后本地应可读 r1: %v", err)
	}
	// 再 pull:已是最新,不传输
	res2, err := local.Pull(ctx, remote, "default", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.UpToDate {
		t.Fatal("无新提交应 up-to-date")
	}
}

func TestM3_PullTransfersOnlyMissing(t *testing.T) {
	ctx := context.Background()
	local, remote, remoteRepo := seedRemoteFastForward(t)
	if _, err := local.Pull(ctx, remote, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := remoteRepo.SetNote(ctx, "r3", NoteInput{Title: "R3", Body: "r3 brand new body"}, "r3"); err != nil {
		t.Fatal(err)
	}
	res, err := local.Pull(ctx, remote, "default", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	// 增量 = 4 个业务对象(blob/note/tree/snapshot)+ 1 个索引根
	// + 4 个受影响分片(r3/brand/new/body 桶,其中 body 桶为重写,r1/r2 桶复用不传)
	if res.Transferred != 9 {
		t.Fatalf("增量应只传 9 个新对象,got %d", res.Transferred)
	}
	if _, err := local.Note(ctx, "r3"); err != nil {
		t.Fatalf("pull 后本地应可读 r3: %v", err)
	}
}

func TestM3_PullDivergeNeedsForce(t *testing.T) {
	ctx := context.Background()
	local, _, _ := newRepo(t, "m3_pull_diverge")
	remote := openRemote(t)
	remoteRepo := Open(remote, Config{Branch: "main", Now: func() int64 { return fixedTime }})
	if _, _, err := remoteRepo.SetNote(ctx, "r1", NoteInput{Title: "R1"}, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Pull(ctx, remote, "default", "main", false); err != nil {
		t.Fatal(err)
	}
	// 分叉:本地加 l1,远端加 r2
	if _, _, err := local.SetNote(ctx, "l1", NoteInput{Title: "L1"}, "l1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := remoteRepo.SetNote(ctx, "r2", NoteInput{Title: "R2"}, "r2"); err != nil {
		t.Fatal(err)
	}
	_, err := local.Pull(ctx, remote, "default", "main", false)
	if err == nil {
		t.Fatal("分叉后 pull 应报错")
	}
	if !errors.Is(err, ErrDiverge) {
		t.Fatalf("期望 ErrDiverge,got %v", err)
	}
	// --force 覆盖
	res, err := local.Pull(ctx, remote, "default", "main", true)
	if err != nil {
		t.Fatalf("force pull 应成功: %v", err)
	}
	if res.FastForward {
		t.Fatal("force 覆盖不应标记 fast-forward")
	}
	if _, err := local.Note(ctx, "r2"); err != nil {
		t.Fatalf("force 后应含 r2: %v", err)
	}
	if _, err := local.Note(ctx, "l1"); err == nil {
		t.Fatal("force 覆盖后本地 l1 应不再存在于当前快照")
	}
}
