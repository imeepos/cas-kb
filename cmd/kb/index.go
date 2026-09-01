package main

import (
	"context"
	"fmt"
)

// cmdIndex 处理 kb index 子命令(目前仅 rebuild)。
func cmdIndex(ctx context.Context, args []string) error {
	if len(args) < 1 || args[0] != "rebuild" {
		return fmt.Errorf("用法: kb index rebuild [-m msg]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"-m": true})
	if err != nil {
		return err
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	msg := f.get("-m", "index rebuild")
	snapAddr, rootAddr, err := r.RebuildIndex(ctx, msg)
	if err != nil {
		return err
	}
	fmt.Printf("index %s\nsnapshot %s\n", rootAddr, snapAddr)
	return nil
}
