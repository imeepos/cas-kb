package main

import "fmt"

func usage() {
	fmt.Print("kb — cas-kb 知识库 CLI\n\n" +
		"用法:\n" +
		"  kb [-p 项目] <命令> …   # 项目作用域;亦可用 KB_PROJECT(默认 default)\n" +
		"  kb init\n" +
		"  kb note set <slug> [--title T] [--body B] [--tags a,b] [-m msg]\n" +
		"  kb note get <slug> [--at 快照]\n" +
		"  kb note rm <slug> [-m msg]\n" +
		"  kb note ls [--json]      # JSON 含 slug/标题/标签/派生摘要\n" +
		"  kb log\n" +
		"  kb diff <base> <tip>     # base/tip 可为分支名、地址或短标识(kb log 首列)\n" +
		"  kb pull [remoteDsn] [--force]\n" +
		"  kb gc\n" +
		"  kb fsck\n" +
		"  kb reset <分支名|地址|短标识>  # 回退分支指针,放弃其后修改\n" +
		"  kb project ls [--json] | create <名称> [--desc 描述] | desc <名称> [描述]\n" +
		"  kb branch ls [--json] | desc <名称> [描述]   # 当前项目作用域内\n\n" +
		"环境变量:\n" +
		"  KB_DSN       本地连接串(默认 postgres://postgres:postgres@127.0.0.1:5432/caskb)\n" +
		"  KB_BRANCH    默认分支名(默认 main)\n" +
		"  KB_REMOTE_DSN pull 的远端连接串(或作为参数传入)\n" +
		"  KB_GC_PROTECT GC 前自动备份分支表(on/off,默认 on)\n" +
		"  KB_PROJECT   项目作用域(默认 default)\n")
}
