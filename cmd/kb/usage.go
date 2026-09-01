package main

import "fmt"

func usage() {
	fmt.Print("kb — cas-kb 知识库 CLI\n\n" +
		"用法:\n" +
		"  kb [-p 项目] <命令> …   # 项目作用域;亦可用 KB_PROJECT(默认 default)\n" +
		"  kb init\n" +
		"  kb note set <路径> [--title T] [--body B] [--tags a,b] [-m msg]\n" +
		"                            # 路径如 task 或 go/concurrency/channel(末段为条目名,父目录自动创建)\n" +
		"  kb note get <路径> [--at 快照]\n" +
		"  kb note rm <路径> [-m msg]\n" +
		"  kb note ls [--dir 目录] [--json]   # 递归列出;JSON 含 path/标题/标签/派生摘要\n" +
		"  kb link resolve <slug> [--at 快照] [--json]  # 解析链接 slug:全路径精确→叶名全库唯一→歧义报错\n" +
		"  kb search <query...> [--at 快照] [-n N] [--json]  # 全文检索(BM25,结果确定性可复现)\n" +
		"  kb dir add <目录> [-m msg]         # 建目录(mkdir -p 语义)\n" +
		"  kb dir ls [目录] [--json]          # 列出直接子项(目录在前)\n" +
		"  kb dir rm <目录> [-m msg] [--force] # 删目录;非空需 --force 递归\n" +
		"  kb dir tree [目录]                 # 层级树(note 附标题);不带 -p/KB_PROJECT 时展示全库视图(项目为顶层)\n" +
		"  kb log\n" +
		"  kb diff <base> <tip>     # base/tip 可为分支名、地址或短标识(kb log 首列)\n" +
		"  kb pull [remoteDsn] [--force]\n" +
		"  kb gc\n" +
		"  kb fsck\n" +
		"  kb index rebuild [-m msg]  # 从当前快照全量重建检索索引(M4)\n" +
		"  kb reset <分支名|地址|短标识>  # 回退分支指针,放弃其后修改\n" +
		"  kb project ls [--json] | create <名称> [--desc 描述] | desc <名称> [描述]\n" +
		"  kb branch ls [--json] | desc <名称> [描述]   # 当前项目作用域内\n" +
		"  kb backup [文件]        # 整库备份为 .ckb(JSONL,全库语义)\n" +
		"  kb restore <文件> [--force]  # 恢复备份;目标非空需 --force(先清空)\n" +
		"  kb wipe [--force]       # 清空整库并重置为全新库;需 --force\n" +
		"  kb update [--yes] [--repo owner/name]  # 检查 GitHub 最新 Release;--yes 下载并替换本二进制\n" +
		"  kb version              # 显示版本号\n\n" +
		"环境变量:\n" +
		"  KB_DSN       库连接串:SQLite 路径(默认 ~/.local/share/caskb/caskb.db;可加 sqlite: 前缀)\n" +
		"               或 postgres://… 切换 PostgreSQL 后端\n" +
		"  KB_BRANCH    默认分支名(默认 main)\n" +
		"  KB_REMOTE_DSN pull 的远端连接串(或作为参数传入)\n" +
		"  KB_GC_PROTECT GC 前自动备份分支表(on/off,默认 on)\n" +
		"  KB_PROJECT   项目作用域(默认 default)\n" +
		"  KB_UPDATE_REPO update 检查的 GitHub 仓库(默认 imeepos/cas-kb)\n" +
		"  GITHUB_TOKEN   可选;update 查询 API 的令牌(缓解匿名限流)\n")
}
