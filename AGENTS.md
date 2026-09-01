# AGENTS.md

> 使用worktree避免冲突，完成后合并到主分支，合并成功后清理老分支确保代码安全，不用用户同意
> 写完的东西要立刻存档（commit）
> 禁止直接在主分支上修改任何代码

本工作区是 **cas-kb 知识库系统**:交付设计文档与 Go 实现(CLI `kb`)。ROADMAP 的 M1–M3.10 已交付(含 M3.5 项目隔离、M3.6 回退与历史读取、M3.7 AI 选用元数据/schema v3、M3.8 目录层级/schema v4:tree 条目带类型,目录可嵌套,条目按全路径定位、M3.9 库级运维命令 backup/restore/wipe、M3.10 存储后端可插拔:SQLite 默认/PostgreSQL 可选);M4(检索/HTTP API)为可选项,未开工。

## 既定事实

- 存储引擎:默认 SQLite 本地文件(modernc.org/sqlite 纯 Go,默认 `~/.local/share/caskb/caskb.db`,KB_DSN 可改路径);可选 Docker 化 PostgreSQL 16(主机 `102` 内网,库名 `caskb`,`KB_DSN=postgres://…` 切换)
- 开发语言:Go(≥1.22),驱动 pgx/v5(PostgreSQL)与 modernc.org/sqlite(SQLite),CLI 名为 `kb`
- 核心架构:内容寻址 + Merkle 树;可变状态收敛于 projects/branches 两张命名空间表(指针+描述),对象不可变
- 文档是权威来源:DESIGN.md(设计)、schema.sql(数据模型规格)、ROADMAP.md(里程碑与验收)

## 约定

- 修改数据模型必须同步三处:schema.sql、DESIGN.md 第 3/4 节、ROADMAP.md 验收标准
- 设计变更在文档内记录,保持「一节一件事」;实现变更须保持文档与实现一致(配置项、行为描述同步更新)
- 提交前统一走质量门禁 `./scripts/verify.sh`(含 gofmt 零输出检查);纯格式变更单独成批提交,不与语义变更混合
- 对含数据的存量库做迁移/升级验证前,必须先 `./scripts/backup.sh` 备份(pg_dump,产物写 backups/,文件名含库版本与时间戳),再行操作
- 交付里程碑/验收批次时,同步更新 ROADMAP 顶部状态行
- 端到端验证统一走 `./scripts/e2e.sh`(临时目录 + 临时库,产物不入库)
- 凭据只写环境变量名(KB_DSN 等),任何文档不得出现真实密码
