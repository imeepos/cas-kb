# Changelog

本文件记录面向用户的显著变更;格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
升级操作指引见 [docs/upgrade.md](docs/upgrade.md)。

## Unreleased

### Added
- 历史保留水位 `kb gc --keep-last K`:按分支深度只保留最近 K 个快照的检索索引,更早快照仅精简索引(数据本体/树/历史条目保留,`note get --at` 仍可用);fsck 按水位豁免其 Index 引用检查;`search --at` 命中被精简快照给可行动报错;水位持久化 meta,普通 gc 沿用
- Markdown 互操作:`kb export md <目录>`(当前分支或 `--at` 历史快照导出为镜像 .md 文件树,front-matter + 正文原文字节,目标已存在整批拒绝并提示 `--force`)与 `kb import md <目录>`(递归扫描,问题文件整批响亮拒绝,一次提交 + 一次索引增量);roundtrip:export(import(X)) 逐字节一致,import(export(库)) 写回零变更(地址不变)

## v0.3.0 - 2026-09-02

### Added
- 批量导入 `kb bulk import <jsonl>`(JSONL 每行 path/title/tags/body):N 条笔记合并为一次提交 + 一次索引批量增量;压测同语料 2000 条由逐条提交 103s/库 6.7GB 降至 350ms/11MB
- 暂存工作流(借鉴 git):`note set/rm`、`dir rm` 加 `--stage` 进入 `<branch>-stage` 暂存分支累积修改(快照不建索引,单条成本恒定);`kb stage` 查看清单,`kb commit` 以暂存基线为参照生成 main 单快照 + 一次索引增量,`kb commit --abort` 丢弃
- 存储透明压缩(SQLite 后端):indexroot/indexshard 对象写入时 gzip、读取自动解压,库体积 −60%(1.45GB→580MB),地址/哈希/上层语义全透明,`.ckb` 备份可移植性不变;`KB_COMPRESS=off` 实验开关

### Changed
- `note ls` 文本模式走轻量元数据读取(不加载正文),2000 条 316ms→30ms

### Fixed
- 分支推进外键失败转译为可行动提示(「项目不存在,请先 kb project create」),覆盖 note/blob 写入、索引重建、pull、reset 四个写路径

### Docs
- DESIGN §7 入档索引三难权衡定论:写快/读快/历史可复现三者取二,现架构取读快 + 历史可复现;段化与分支缓存两案被否的量化依据与触发条件

## v0.2.0 - 2026-09-01

### Added
- 全文检索 `kb search`(BM25,字段加权:标题 3/标签 2/正文 1;结果确定性可复现,支持 `--at` 历史快照检索与 `--json`)
- 倒排索引纳入快照(新对象 kind:indexroot/indexshard,snapshot.index 引用;结构共享式增量重建)
- `kb index rebuild`:从当前快照全量重建检索索引(旧库升级/自愈用)
- `kb link resolve <slug>`:链接 slug 解析(全路径精确 → 叶名全库唯一 → 歧义列候选)
- `store.ProjectGet` 契约;`project desc <名>` 查询改为点查

### Changed
- **库 schema v5**:objects.kind 约束放宽(新增 indexroot/indexshard);**v4 及更早库需清库重建或走备份恢复升级**(见 docs/upgrade.md)
- `kb restore` 接受 header schema_version ∈ [4, 当前] 的备份(v4 起对象编码兼容),恢复后提示重新 backup 完成升级;v3 及更早与未来版本仍拒绝
- 质量门禁 `verify.sh` 串联端到端 e2e;CLI 集成测试默认 SQLite 零依赖(KB_TEST_DSN 走 PG 回归)

### Fixed
- 升级断点:v0.1.x 备份在新版无法恢复的问题(跨版本恢复门禁放宽)
- `project desc <名>` 查询由全表扫描改为按名点查

## v0.1.1 - 2026-09-01

### Fixed
- dir tree 全库视图与若干稳定性修复(M3.11)

## v0.1.0 - 2026-09-01

### Added
- 首个公开发布:内容寻址存储内核(哈希/对象/存储三层)、知识条目与版本(快照 DAG/diff/回退)、同步与运维(pull/gc/fsck/backup/restore/wipe)、项目隔离、AI 选用元数据、目录层级、SQLite 默认 + PostgreSQL 可选后端、`kb update` 在线自更新
