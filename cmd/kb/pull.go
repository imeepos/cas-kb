package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

func cmdPull(ctx context.Context, args []string) error {
	force := false
	var remoteDsn string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		remoteDsn = a
	}
	if remoteDsn == "" {
		remoteDsn = os.Getenv("KB_REMOTE_DSN")
	}
	if remoteDsn == "" {
		return fmt.Errorf("pull: 缺少远端 DSN(参数或 KB_REMOTE_DSN)")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	remote, err := store.Open(ctx, remoteDsn)
	if err != nil {
		return err
	}
	defer remote.Close()
	res, err := r.Pull(ctx, remote, branchName(), force)
	if err != nil {
		if errors.Is(err, repo.ErrDiverge) {
			return fmt.Errorf("pull: %w,需要 --force 才能覆盖", err)
		}
		return err
	}
	if res.UpToDate {
		fmt.Println("已是最新")
		return nil
	}
	verb := "fast-forward"
	if !res.FastForward {
		verb = "force"
	}
	fmt.Printf("已同步 %d 个对象(%s)\n", res.Transferred, verb)
	fmt.Printf("  %s -> %s\n", shortAddr(res.From), shortAddr(res.To))
	return nil
}
