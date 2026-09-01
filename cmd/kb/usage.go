package main

import "fmt"

func usage() {
	fmt.Print("kb — cas-kb 知识库 CLI\n\n" +
		"用法:\n" +
		"  kb init\n" +
		"  kb note set <slug> [--title T] [--body B] [--tags a,b] [-m msg]\n" +
		"  kb note get <slug>\n" +
		"  kb note rm <slug> [-m msg]\n" +
		"  kb note ls\n" +
		"  kb log\n" +
		"  kb diff <base> <tip>\n" +
		"  kb pull [remoteDsn] [--force]\n" +
		"  kb gc\n" +
		"  kb fsck\n\n" +
		"环境变量:\n" +
		"  KB_DSN       本地连接串(默认 postgres://postgres:postgres@127.0.0.1:5432/caskb)\n" +
		"  KB_BRANCH    默认分支名(默认 main)\n" +
		"  KB_REMOTE_DSN pull 的远端连接串(或作为参数传入)\n" +
		"  KB_GC_PROTECT GC 前自动备份分支表(on/off,默认 on)\n")
}
