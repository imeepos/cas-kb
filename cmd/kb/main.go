// Command kb 是 cas-kb 知识库的 CLI 入口。
package main

import (
	"context"
	"fmt"
	"os"
)

// extractProjectArg 抽取任意位置的 "-p 项目" 参数,返回剩余参数与项目名。
// 注意:-p 为全局保留参数,不能作为命令位置参数或旗标值使用。
func extractProjectArg(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	proj := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-p" && i+1 < len(args) {
			proj = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out, proj
}

func main() {
	args, proj := extractProjectArg(os.Args[1:])
	projectOverride = proj
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
	case "dir":
		must(cmdDir(ctx, args[1:]))
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
	case "reset":
		must(cmdReset(ctx, args[1:]))
	case "project":
		must(cmdProject(ctx, args[1:]))
	case "branch":
		must(cmdBranch(ctx, args[1:]))
	case "backup":
		must(cmdBackup(ctx, args[1:]))
	case "restore":
		must(cmdRestore(ctx, args[1:]))
	case "wipe":
		must(cmdWipe(ctx, args[1:]))
	case "help", "--help", "-h":
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
