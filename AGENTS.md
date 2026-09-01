# AGENTS.md

本工作区是 **cas-kb 知识库系统**:交付设计文档与 Go 实现(CLI `kb`)。ROADMAP 的 M1–M3 已交付;M4(检索/HTTP API)为可选项,未开工。

## 既定事实

- 存储引擎:Docker 化 PostgreSQL 16,部署在主机 `102`(内网),库名 `caskb`
- 开发语言:Go(≥1.22),驱动 pgx/v5,CLI 名为 `kb`
- 核心架构:内容寻址 + Merkle 树;唯一可变状态是 branches 表
- 文档是权威来源:DESIGN.md(设计)、schema.sql(数据模型规格)、ROADMAP.md(里程碑与验收)

## 约定

- 修改数据模型必须同步三处:schema.sql、DESIGN.md 第 3/4 节、ROADMAP.md 验收标准
- 设计变更在文档内记录,保持「一节一件事」;实现变更须保持文档与实现一致(配置项、行为描述同步更新)
- 提交前统一走质量门禁 `./scripts/verify.sh`(含 gofmt 零输出检查);纯格式变更单独成批提交,不与语义变更混合
- 凭据只写环境变量名(KB_DSN 等),任何文档不得出现真实密码
