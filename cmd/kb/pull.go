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
	merge := false
	var remoteDsn string
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "--merge":
			merge = true
		default:
			remoteDsn = a
		}
	}
	if force && merge {
		return errors.New("pull: --force 与 --merge 互斥,请二选一(--force 覆盖回退,--merge 三方合并)")
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
	if !merge {
		res, err := r.Pull(ctx, remote, projectName(), branchName(), force)
		if err != nil {
			if errors.Is(err, repo.ErrDiverge) {
				// 分叉提示追加三方合并指引(调研 §2.7):默认行为不变,
				// 给用户渐进迁移路径
				return fmt.Errorf("pull: %w,需要 --force 才能覆盖;或改用 kb pull --merge 做三方合并", err)
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
	// --merge:三方合并(判定矩阵见调研 §2.7;冲突建 <branch>-merge 中间态)
	res, err := r.MergeStart(ctx, remote, projectName(), branchName(), repo.MergeOptions{})
	if err != nil {
		var mc *repo.ErrMergeConflicts
		if errors.As(err, &mc) {
			// 冲突清单(路径 + 判定类别)输出到 stdout,错误走 stderr,
			// 退出码非零;中间态分支与 meta 键已由 MergeStart 建立
			fmt.Printf("已同步 %d 个对象(merge)\n", res.Transferred)
			fmt.Printf("分叉:base %s  ours %s  theirs %s\n", shortAddr(res.Base), shortAddr(res.Ours), shortAddr(res.Theirs))
			fmt.Printf("自动合并 %d 条;冲突 %d 条:\n", res.AutoMerged, len(res.Conflicts))
			for _, c := range res.Conflicts {
				fmt.Printf("  %s  %s  base %s  ours %s  theirs %s\n", c.Path, c.Kind, shortAddr(c.Base), shortAddr(c.Ours), shortAddr(c.Theirs))
			}
			fmt.Printf("已建中间态分支 %s:逐条 kb note set/rm <路径> --stage 裁决后 kb merge --continue;或 kb merge --abort 放弃\n", branchName()+"-merge")
			return errors.New("pull: 合并检出冲突,未落库")
		}
		return err
	}
	if res.UpToDate {
		fmt.Println("已是最新")
		return nil
	}
	if res.FastForward {
		fmt.Printf("已同步 %d 个对象(fast-forward)\n", res.Transferred)
		fmt.Printf("  %s -> %s\n", shortAddr(res.Ours), shortAddr(res.Theirs))
		return nil
	}
	fmt.Printf("已同步 %d 个对象(merge)\n", res.Transferred)
	fmt.Printf("自动合并 %d 条;冲突 0 条\n", res.AutoMerged)
	fmt.Printf("合并快照 %s(parents %s %s)\n", res.Snap, shortAddr(res.Ours), shortAddr(res.Theirs))
	return nil
}
