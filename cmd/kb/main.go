// Command kb 是 cas-kb 知识库的 CLI 入口。
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "-p" {
		projectOverride = args[1]
		args = args[2:]
	}
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	switch args[0] {
	case "init":
		must(cmdInit(ctx, args[1:]))
	case "note":
		must(cmdNote(ctx, args[1:]))
	case "log":
		must(cmdLog(ctx, args[1:]))
	case "diff":
		must(cmdDiff(ctx, args[1:]))
	case "pull":
		must(cmdPull(ctx, args[1:]))
	case "gc":
		must(cmdGC(ctx, args[1:]))
	case "fsck":
		must(cmdFSCK(ctx, args[1:]))
	case "project":
		must(cmdProject(ctx, args[1:]))
	case "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "kb: 未知命令 %q\n", args[0])
		usage()
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		os.Exit(1)
	}
}
