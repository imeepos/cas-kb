package main

import (
	"context"
	"fmt"
	"os"
)

func cmdFSCK(ctx context.Context, args []string) error {
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := r.FSCK(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("FSCK: 检查 %d 个对象\n", res.Checked)
	for _, p := range res.Problems {
		fmt.Printf("  [%s] %s: %s\n", p.Kind, p.Addr, p.Problem)
	}
	if len(res.Problems) > 0 {
		fmt.Fprintf(os.Stderr, "发现 %d 个问题\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Println("完整,无问题")
	return nil
}
