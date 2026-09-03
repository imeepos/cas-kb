# AGENTS.md

> 使用worktree避免冲突，完成后合并到主分支，合并成功后清理老分支确保代码安全，不用用户同意
> 写完的东西要立刻存档（commit）
> 禁止直接在主分支上修改任何代码

## 会话心跳纪律

commit 即心跳:每个工作步(一次调研、一个可验证改动)完成立刻存档;无法 commit 的长动作(外网等待、长测试)不得超过 30 分钟,超时应拆步。三条阈值,成文不靠感觉:

- **心跳间隔 ≤ 30 分钟**(一个工作步的上限)
- **死亡阈值 = 分支最近 commit 年龄 > 90 分钟**(3× 心跳间隔,即「连续三拍缺席」)
- **会话墙钟上限 6 小时**(超限即使仍在产出也应收束,或显式声明展期——显式说出,不留默认)

死亡判定走 `./scripts/session-watch.sh`(退出码说话):**判定归脚本,处置归负责人**——不自动删分支、不自动合并。被判死亡的分支进入**冻结名单**(不新写),归档或重启由负责人决定;重启 = 新分支 + 走复活协议,**不原地复活旧分支**(旧分支 tip 停在死亡时刻,本身就是事故现场的勘验证据)。

**复活 ≠ 续写原计划**。原会话复活/重建开工前必走三查,任一查不过即转「已交付/归档」分支处理:

1. **查已交付**:ROADMAP 顶部状态行与对应里程碑小节、CHANGELOG(Unreleased 与已定版)、`git log --all --grep=<里程碑关键词>`
2. **查分支关系**:`git branch --merged main` 与 `git log --oneline main..<分支>`;分支已并入 main 时,复活的唯一合法动作是**归档**,不产生任何新实现
3. **查撞车**:`git diff <复活分支>…main -- <目标文件>`;main 已有同范围实现时,**复用优先于重写**——差距用小步补丁/cherry-pick 表达,禁止按旧计划重写第二套

复活产出的首个 commit message 必须注明「复活自 Txx,已核对 main@<sha> 无重复实现」(审计锚点);入库前照走 `./scripts/verify.sh` 门禁与「文档三处同步」纪律。

(依据:docs/research/ops-patterns.md §1.3,T57——T54 复活补交撞车报废事故的根因修复)

本工作区是 **cas-kb 知识库系统**:交付设计文档与 Go 实现(CLI `kb`)。ROADMAP 的 M1–M3.11 与 M4 CLI 已交付(含 M3.5 项目隔离、M3.6 回退与历史读取、M3.7 AI 选用元数据/schema v3、M3.8 目录层级/schema v4:tree 条目带类型,目录可嵌套,条目按全路径定位、M3.9 库级运维命令 backup/restore/wipe、M3.10 存储后端可插拔:SQLite 默认/PostgreSQL 可选、M3.11 dir tree 全库视图、M4 CLI 检索/schema v5:`kb search`、倒排索引纳入快照、`kb index rebuild`、`kb link resolve`;另已交付 bulk 批量导入、暂存工作流、SQLite 索引对象透明压缩);HTTP API 为 M4 可选项,未开工。

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
