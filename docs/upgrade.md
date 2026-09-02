# 升级指南

## 从 v0.1.x(schema v4)升级到 v0.2.0(schema v5)

> 新版**无法直接打开**旧库文件——这是刻意的响亮失败,不做自动迁移。
> 数据迁移走备份恢复路径,全程数据不丢:

1. **用旧版 kb 导出备份**(就用你现在装着的 v0.1.x):
   ```bash
   kb backup
   ```
   产物为 `caskb-v4-backup-<日期>.ckb`。
2. **让新版接管**:把 v0.2.0 的 kb 备好后,初始化一个全新库再恢复备份:
   ```bash
   kb init
   kb restore caskb-v4-backup-<日期>.ckb --force
   ```
   v4 备份的对象与当前版本逐字节兼容,恢复会提示「备份来自 schema v4」。
3. **补建检索索引**(v0.2.0 新增全文检索,旧快照没有索引):
   ```bash
   kb index rebuild
   ```
4. **重新导出备份**,完成备份升级:
   ```bash
   kb backup
   ```

## 注意事项

- v3 及更早版本的备份与当前对象编码不兼容,请先用配套旧版 kb 恢复,再按上面步骤升级;
- PostgreSQL 后端同理(`KB_DSN` 指向 PG 时步骤一致);若偏好数据库级备份,可用 `scripts/backup.sh`(pg_dump);
- 恢复完成后建议运行 `kb fsck` 复核。
