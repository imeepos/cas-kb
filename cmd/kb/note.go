package main

import (
	"context"
	"fmt"
)

// cmdNote 分发 note 子命令。
func cmdNote(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("note: 缺少子命令(set/get/rm/ls)")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "set":
		return noteSet(ctx, r, args[1:])
	case "get":
		return noteGet(ctx, r, args[1:])
	case "rm":
		return noteRm(ctx, r, args[1:])
	case "ls":
		return noteLs(ctx, r, args[1:])
	default:
		return fmt.Errorf("note: 未知子命令 %q", args[0])
	}
}

// validNoteFlags 声明 note set 需要取值的旗标。
var validNoteFlags = map[string]bool{"--title": true, "--body": true, "--tags": true, "-m": true, "--msg": true, "--time": true}
