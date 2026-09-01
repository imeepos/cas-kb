// Command kb 是 cas-kb 知识库的 CLI 入口。
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "init":
		must(cmdInit(ctx, os.Args[2:]))
	case "note":
		must(cmdNote(ctx, os.Args[2:]))
	case "log":
		must(cmdLog(ctx, os.Args[2:]))
	case "diff":
		must(cmdDiff(ctx, os.Args[2:]))
	case "pull":
		must(cmdPull(ctx, os.Args[2:]))
	case "gc":
		must(cmdGC(ctx, os.Args[2:]))
	case "fsck":
		must(cmdFSCK(ctx, os.Args[2:]))
	case "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "kb: 未知命令 %q\n", os.Args[1])
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
