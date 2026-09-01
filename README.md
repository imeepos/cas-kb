# cas-kb — 基于内容寻址与 Merkle 树的知识库

一句话定位:以**内容寻址存储(CAS)+ Merkle 树**为核心的知识库系统。
存储引擎为 PostgreSQL(生产部署于主机 `102`),开发语言 **Go**,交付物为 CLI `kb`。
本仓库包含设计文档、数据模型规格与实现代码:ROADMAP 的 **M1–M3.6 已交付**(存储内核、条目与版本、同步与运维、项目隔离、回退与历史读取),M4(检索/HTTP API)为可选、未开工。

## 文档导航

| 文档 | 内容 |
|---|---|
| [DESIGN.md](DESIGN.md) | 完整设计:对象模型、存储设计、同步协议、GC、检索、部署与权衡 |
| [schema.sql](schema.sql) | PostgreSQL 数据模型 DDL 规格(schema v3,含项目隔离与 AI 选用描述) |
| [ROADMAP.md](ROADMAP.md) | 落地路线图:M1–M4 里程碑与验收标准 |

## 核心思想(30 秒版)

- 每条知识按内容哈希寻址:`地址 = sha256(规范字节)`,对象一旦写入不可变
- Merkle 树把「条目 → 目录 → 快照」层层哈希,**一个根哈希代表全库状态**
- 全库唯一的可变状态 = 分支指针表(`branches: (项目, 名字) → 快照地址`)
- 版本历史 = 快照 DAG;同步 = 比较哈希、只传缺失对象;完整性 = 地址即校验和

## 已交付能力(M1–M3.6)

- **M1 存储内核**:hash / object / store 三层 + Postgres 迁移(`kb init`)
- **M2 条目与版本**:`kb note set|get|rm|ls`、`kb log`、`kb diff`(支持分支名、快照地址或日志短标识)
- **M3 同步与运维**:`kb pull`(祖先检查 /`--force`)、`kb gc`(清扫前自动备份分支表)、`kb fsck`
- **M3.5 项目隔离**:同库多项目互不可见(`-p 项目` / `KB_PROJECT` / `kb project ls|create`)
- **M3.6 回退与历史读取**:`kb reset <短标识>` 放弃其后修改;`note get --at <快照>` 读取历史版本

## 快速开始

    go build -o kb ./cmd/kb
    export KB_DSN=postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable
    ./kb init
    ./kb note set hello --title 你好 --body "第一条笔记"
    ./kb log
    ./kb project create notes
    ./kb -p notes note set idea --title 点子 --body "另一个项目里"
    ./kb -p notes reset <短标识>          # 放弃其后修改
    ./kb -p notes note get idea --at <快照>  # 读取历史版本

## 开发与测试

    ./scripts/verify.sh                                  # 单一质量门禁:gofmt/构建/vet/单元/(可选)集成
    KB_TEST_DSN=postgres://... ./scripts/verify.sh       # 设置后追加集成测试;每个用例派生独立临时库
    ./scripts/e2e.sh                                     # 端到端验收:临时目录+临时库跑完整生命周期

## 环境假设

| 项 | 值 |
|---|---|
| 数据库 | PostgreSQL 16,生产 Docker 部署于主机 `102`(如 192.168.x.102:5432);本地开发用任意可达实例 |
| 数据库名 | `caskb` |
| 开发语言 | Go ≥ 1.22,驱动 pgx/v5 |
| 接入形态 | CLI 优先(`kb` 命令),HTTP API 列为 M4 可选 |

连接串、账号与安全要求见 [DESIGN.md](DESIGN.md) 第 8 节;配置项清单见第 8.2 节。
