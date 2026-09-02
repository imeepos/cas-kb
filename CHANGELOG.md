# Changelog

本文件记录面向用户的显著变更;格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
升级操作指引见 [docs/upgrade.md](docs/upgrade.md)。

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
