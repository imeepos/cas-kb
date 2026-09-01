package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/repo"
)

// cmdReset 把当前分支指针回退到历史快照(放弃其后修改)。
func cmdReset(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("reset: 需要 <分支名|地址|短标识> 一个参数")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := r.Reset(ctx, args[0])
	if errors.Is(err, repo.ErrResetTargetNotAncestor) {
		return fmt.Errorf("reset: %w(只能回退到当前头的历史快照)", err)
	}
	if err != nil {
		return err
	}
	fmt.Printf("已回退 %s -> %s(放弃 %d 个提交)\n", shortAddr(res.From), shortAddr(res.To), res.Abandoned)
	if res.Abandoned > 0 {
		fmt.Println("被放弃的历史在下次 gc 前仍可通过完整地址访问;回退后再 pull 同一远端会被 fast-forward 推回")
	}
	return nil
}
