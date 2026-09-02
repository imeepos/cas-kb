# cas-kb — 基于内容寻址与 Merkle 树的知识库

一句话定位:以**内容寻址存储(CAS)+ Merkle 树**为核心的知识库系统。
存储引擎**默认 SQLite 本地文件**(零依赖开箱即用),`KB_DSN=postgres://…` 可切换 PostgreSQL(生产部署于主机 `102`);开发语言 **Go**,交付物为 CLI `kb`。
本仓库包含设计文档、数据模型规格与实现代码:ROADMAP 的 **M1–M3.11 与 M4 CLI 已交付**(存储内核、条目与版本、同步与运维、项目隔离、回退与历史读取、AI 选用元数据、目录层级、库级运维命令、双后端、全文检索与倒排索引),HTTP API(M4 收尾只读 + M4.1 写入型,令牌鉴权、默认只读)已交付。

## 文档导航

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | 完整设计:对象模型、存储设计、同步协议、GC、检索、部署与权衡 |
| [schema.sql](schema.sql) | PostgreSQL 数据模型 DDL 规格(schema v5,含项目隔离、AI 选用描述与目录层级) |
| [schema_sqlite.sql](schema_sqlite.sql) | SQLite 数据模型 DDL(schema.sql 的语义镜像,双后端共用 schema v5) |
| [ROADMAP.md](ROADMAP.md) | 落地路线图:M1–M4 里程碑与验收标准 |

## 核心思想(30 秒版)

- 每条知识按内容哈希寻址:`地址 = sha256(规范字节)`,对象一旦写入不可变
- Merkle 树把「条目 → 目录 → 快照」层层哈希,**一个根哈希代表全库状态**
- 目录即子树:tree 条目带类型(note|dir),目录可任意嵌套;改一条笔记只重写「它 + 祖先目录链 + 新快照」,兄弟子树地址结构共享
- 全库唯一的可变状态 = 分支指针表(`branches: (项目, 名字) → 快照地址`)
- 版本历史 = 快照 DAG;同步 = 比较哈希、只传缺失对象;完整性 = 地址即校验和

## 已交付能力(M1–M5 CLI)

- **M1 存储内核**:hash / object / store 三层 + 迁移与版本门禁(`kb init`)
- **M2 条目与版本**:`kb note set|get|rm|ls`、`kb log`、`kb diff`(支持分支名、快照地址或日志短标识)
- **M3 同步与运维**:`kb pull`(祖先检查 /`--force`)、`kb gc`(清扫前自动备份分支表)、`kb fsck`
- **M3.5 项目隔离**:同库多项目互不可见(`-p 项目` / `KB_PROJECT` / `kb project ls|create`)
- **M3.6 回退与历史读取**:`kb reset <短标识>` 放弃其后修改;`note get --at <快照>` 读取历史版本
- **M3.7 AI 选用元数据**:项目/分支描述(`project create --desc`、`project desc`、`branch desc`)与 `project|branch ls --json` 机器可读清单;`note ls --json` 含派生摘要(schema v3)
- **M3.8 目录层级**:条目按全路径定位(`note set go/concurrency/channel`),目录可嵌套(`kb dir add|ls|rm|tree`,mkdir -p 语义、非空删除需 `--force`);树对象编码演进为带类型条目(schema v4)
- **M3.9 库级运维**:`kb backup|restore`(原生 .ckb,跨后端可移植、逐对象哈希校验、非空恢复需 `--force`)、`kb wipe --force`(重置为全新库)
- **M3.10 双后端**:默认 SQLite 文件库(零外部依赖);`KB_DSN=postgres://…` 切换 PostgreSQL;两后端同一套测试与 e2e 全绿,.ckb 备份可跨后端迁移
- **M3.11 dir tree 全库视图**:不带 `-p` 时渲染全库树(项目为顶层节点,逐项目挂默认分支树)
- **M4 CLI 检索**(schema v5):BM25 全文检索 `kb search`(字段加权 标题3/标签2/正文1,结果确定性可复现,`--at` 历史快照检索);`--snippet` 附命中片段(M4.2 展示层增量,命中词元以【】包裹,评分/排序零变化);倒排索引纳入快照(indexroot/indexshard,结构共享式增量);`kb index rebuild` 全量重建;`kb link resolve` 链接解析
- **批量导入**:`kb bulk import <jsonl>` N 条笔记一次提交 + 一次索引增量(2000 条由逐条 103s/6.7GB 降至 350ms/11MB)
- **Markdown 互操作**:`kb export md <目录>` 当前分支或 `--at` 历史快照导出为镜像 .md 文件树(front-matter + 正文原文字节,已存在整批拒绝、`--force` 覆盖);`kb import md <目录>` 递归导入(title 必填、tags 逗号分隔,问题文件整批响亮拒绝,一次提交一次索引增量);roundtrip 逐字节一致,写回零变更(地址不变)
- **暂存工作流**:`note set/rm`、`dir rm --stage` 累积到暂存分支(单条成本恒定),`kb stage` 查看清单、`kb commit` 合入、`kb commit --abort` 丢弃
- **存储透明压缩**:SQLite 索引对象写入 gzip、读取透明解压,库体积 −60%;`KB_COMPRESS=off` 可关
- **只读 HTTP API**(M4 收尾,DESIGN §8.5):`kb serve` 默认只绑 127.0.0.1:8787,暴露 `/healthz` 与 `/api/v1/{projects,tree,note,search,log,diff}`(全部 GET);JSON 与 CLI `--json` 同一份契约(internal/view,TestServeCLIParity 逐字段钉死)
- **写入型 HTTP API**(M4.1,DESIGN §8.6):`POST/DELETE /api/v1/note` 等价 `kb note set/rm`(复用 repo.SetNote/RemoveNote);`--token <值>` 或 `KB_SERVE_TOKEN` 启用写端点(内存常量时间比较、不写日志不回显),**未配置令牌时保持纯只读(写端点一律 403)**;锁忙返回 503 并提示稍后重试或改用 CLI;`kb note get` 补 `--json`(TestServeWriteCLIParity 逐字段钉死)
- **M5 三方合并**:`kb pull --merge`(与 `--force` 互斥)分叉时按最近公共祖先做条目级三方合并(单侧变取单侧、双侧同变自动合、Merkle 剪枝;不做行级合并)——零冲突直接落双亲合并快照(历史双侧可达),冲突全有或全无:`<branch>-merge` 中间态 + 冲突清单(退出码非零);合并中态冻结该分支直接写,`note set/rm --stage` 升格为裁决,`kb merge --continue` 落合并快照收束、`kb merge --abort` 回到合并前;`pull` 本地领先修正为「已是最新」空操作

## 快速开始

    go build -o kb ./cmd/kb              # 拉取新代码后记得重建二进制
    ./kb init                            # 默认 SQLite:库文件 ~/.local/share/caskb/caskb.db(schema v5;旧版本库会拒绝并提示重建)
    ./kb --help                          # 完整命令清单

> 运维与安全(部署形态、令牌管理、备份维护、systemd/launchd 示例)见 [docs/serve.md](docs/serve.md)。

    # 可选:切换 PostgreSQL 后端(生产 102 主机或本地任意可达实例)
    # export KB_DSN=postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable

写入目录与条目(条目按全路径定位,父目录自动创建):

    ./kb dir add go                      # 建目录(mkdir -p 语义,重复执行幂等)
    ./kb note set go/concurrency/channel --title 通道 --body "chan 语义"
    ./kb note set hello --title 你好 --body "第一条笔记"

查看层级与内容:

    ./kb dir tree                        # 层级树(note 附标题)
    # (root)/
    # ├── go/
    # │   └── concurrency/
    # │       └── channel  通道
    # └── hello  你好
    ./kb dir ls go                       # 直接子项(目录在前);--json 机器可读
    ./kb note ls                         # 全库递归,路径列
    ./kb note get go/concurrency/channel # 按全路径读(首行输出 path:)

检索(片段便于人眼/AI 快速判断相关性;纯展示层,排序与命中集合零变化):

    ./kb search chan                     # BM25 全文检索,结果确定性可复现
    ./kb search chan --snippet           # 命中行下附缩进片段,命中词元以【】包裹
    ./kb search 通道 --json --snippet    # 机器可读;snippet 为可选字段,缺省不带

版本与变更:

    ./kb log                             # 快照链,首列短标识
    ./kb diff <短标识> main              # 按全路径输出 A/D/M;目录间移动 = 旧路径 D + 新路径 A
    ./kb note get go/concurrency/channel --at <短标识>   # 历史版本
    ./kb reset <短标识>                  # 指针回拨,放弃其后提交

多机同步与三方合并(两台机器互为远端,分叉不再只有 --force 一条路):

    ./kb pull sqlite:/data/other/caskb.db --merge   # 分叉时三方合并;零冲突直接落双亲合并快照
    ./kb pull sqlite:/data/other/caskb.db --merge --allow-unrelated   # 冷启动:两库各自 init 无共同历史,空基线合并(两侧新增互不冲突即全取)
    # 冲突时退出码非零,输出冲突清单并建 <branch>-merge 中间态(原分支指针不动)
    ./kb stage                            # 合并中态:查看冲突清单与裁决进度
    ./kb note set task --title 通道 --body "合并稿" --stage   # 逐条裁决(或 note rm --stage 接受删除)
    ./kb merge --continue -m "merge theirs:裁决说明"           # 收束:双亲合并快照 + 清理中间态
    ./kb merge --abort                    # 或放弃:删中间态,回到合并前

冷启动(两库各自 init,无共同历史)三步——两台机器各自生长过、首次同步时走这里(独立历史是正常起点,不是错误):

    # 第 1 步 自证:两台机器各自 kb init + 写入,各自形成独立历史
    # 第 2 步 对拉:任意一台执行下面这句(先 pull 的一台合并后,对端再 pull 即走 fast-forward——两次 pull 各一次,第二台不再需要旗标)
    ./kb pull sqlite:/data/other/caskb.db --merge --allow-unrelated   # 空基线合并:两侧新增互不冲突即全取
    # 第 3 步 收敛:零冲突直接落双亲合并快照,输出提示「冷启动完成:两侧历史已建立共同祖先,后续 pull 无需 --allow-unrelated」,此后恢复正常同步语义

目录删除与运维:

    ./kb dir rm go                       # 非空目录 → 拒绝并提示 --force
    ./kb dir rm go --force               # 递归删除整棵子树
    ./kb gc && ./kb fsck                 # 回收不可达对象 + 全库巡检

在线更新(Release 版二进制):

    ./kb version                         # 当前版本与平台
    ./kb update                          # 在线检查 GitHub 最新 Release
    ./kb update --yes                    # 下载、校验 sha256 并替换本二进制

多项目作用域:

    ./kb project create notes --desc "另一个知识域"
    ./kb project ls --json               # 机器可读项目清单(AI 选用入口)
    ./kb -p notes note set idea --title 点子 --body "另一个项目里"

与编辑器/Obsidian 互通(Markdown 互操作):

    ./kb export md ~/notes               # 当前分支全部条目 → 镜像 .md 文件树(--at 可读历史快照)
    ./kb import md ~/notes               # 递归导入 .md 目录(front-matter:title/tags;一次提交一次索引增量)

HTTP API(AI/Agent 免 shell 消费与写入;DESIGN §8.5/§8.6):

    ./kb serve                           # 默认 127.0.0.1:8787,只绑回环;Ctrl-C 优雅退出
    ./kb serve --addr 127.0.0.1:9000     # 换端口;跨机消费走 SSH 端口转发或反向代理
    curl -s localhost:8787/healthz                       # {"ok": true, "backend": "sqlite", ...}
    curl -s 'localhost:8787/api/v1/note?path=hello'      # 单条笔记(正文 + 派生摘要)
    curl -s 'localhost:8787/api/v1/search?q=chan&limit=5' # 检索,与 kb search --json 同构
    curl -s 'localhost:8787/api/v1/search?q=chan&snippet=1' # 附 snippet 片段字段(命中词元以【】包裹)

写入模式(默认只读;配置令牌后启用 POST/DELETE,DESIGN §8.6):

    ./kb serve --token <token>           # 或 export KB_SERVE_TOKEN=<token>(--token 优先)
    curl -s -X POST 'localhost:8787/api/v1/note' \      # 等价 kb note set;201 + {path,address,short}
         -H "Authorization: Bearer <token>" -H 'Content-Type: application/json' \
         -d '{"path":"go/concurrency/channel","title":"通道","tags":["go"],"body":"chan 语义"}'
    curl -s -X DELETE -H "Authorization: Bearer <token>" \  # 等价 kb note rm;200 + {removed,short}
         'localhost:8787/api/v1/note?path=go/concurrency/channel'
    # 未配置令牌:写端点一律 403(纯只读);缺头/错令牌 401;与 CLI 同时写遇锁忙 503

## 开发与测试

    ./scripts/verify.sh                                  # 单一质量门禁:gofmt/构建/vet/测试(默认含 SQLite 全套集成)
    KB_TEST_DSN=postgres://... ./scripts/verify.sh       # 设置后追加 PostgreSQL 集成回归;每个用例派生独立临时库
    ./scripts/e2e.sh                                     # 端到端验收(默认 SQLite 临时库):完整生命周期
    ./scripts/e2e.sh postgres://...                      # 同一生命周期跑 PostgreSQL 后端(含 pg_dump 备份路径)
    ./scripts/backup.sh [DSN]                            # 库备份 → backups/(git 忽略),文件名含库版本与时间戳
    ./scripts/restore.sh <backup.sql> <目标库>           # 恢复到全新库;旧 schema 备份会提示配套二进制

> 提示:仓库根目录的 `kb` 二进制不会随源码自动更新,`git pull` 后执行 `go build -o kb ./cmd/kb` 重建;Release 安装的二进制则可用 `./kb update --yes` 在线升级;`./kb --help` / `-h` 随时查看当前用法。

## 构建发布

推送 `v*` 标签即自动走 GitHub Actions(`.github/workflows/release.yml`)发布:

    git tag v0.1.0 && git push origin v0.1.0

流水线三步:**质量门禁**(`verify.sh` 含集成测试 + `e2e.sh`,自带 postgres:16 服务)→
**交叉编译**(linux / darwin / windows × amd64 / arm64,CGO 关闭的静态二进制,
产物 `kb-<版本>-<os>-<arch>` 归档)→ **创建 GitHub Release**(自动生成更新说明,
附 `sha256sums.txt`)。Actions 页也支持手动 `workflow_dispatch` 试跑(只构建、不发布)。
构建以 `-ldflags "-X main.version=<tag>"` 注入版本号:`kb version` 显示它,`kb update` 靠它与最新 Release 比较;本地 `go build` 的产物版本为 `dev`,只能查看、不参与比较。

## 环境假设

| 项 | 值 |
|---|---|
| 数据库 | 默认 SQLite(库文件 `~/.local/share/caskb/caskb.db`,`KB_DSN` 可改);可选 PostgreSQL 16,生产 Docker 部署于主机 `102`(如 192.168.x.102:5432) |
| 数据库名 | SQLite 无库名(单文件);PG 为 `caskb` |
| 开发语言 | Go ≥ 1.22,驱动 pgx/v5(PostgreSQL)与 modernc.org/sqlite(SQLite,纯 Go 无 CGO) |
| 接入形态 | CLI 优先(`kb` 命令),HTTP API 列为 M4 可选 |

连接串、账号与安全要求见 [DESIGN.md](DESIGN.md) 第 8 节;配置项清单见第 8.2 节。
