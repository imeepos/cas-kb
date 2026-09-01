#!/bin/bash
# cas-kb 库备份统一入口:产物固定写 backups/(git 忽略),文件名含库 schema 版本与时间戳。
# 用法:./scripts/backup.sh [库 DSN](默认取 KB_DSN,再退回本地 5432/caskb)
set -euo pipefail
cd "$(dirname "$0")/.."

# Homebrew 工具(psql/pg_dump)可能不在非交互 shell 的 PATH 里,逐层补齐
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin \
          /opt/homebrew/opt/postgresql@16/bin /opt/homebrew/opt/postgresql@17/bin /opt/homebrew/opt/postgresql@18/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH

DSN="${1:-${KB_DSN:-postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable}}"
command -v pg_dump >/dev/null || { echo "backup: 需要 pg_dump"; exit 1; }

# 记录库当前 schema 版本进文件名,恢复时可识别配套的二进制版本
VER=$(psql "$DSN" -tAc "SELECT value FROM meta WHERE key = 'schema_version'" 2>/dev/null | head -1 | tr -d "[:space:]" || true)
[ -n "$VER" ] || { echo "backup: 无法读取库版本(连接失败或库未初始化): $DSN"; exit 1; }

mkdir -p backups
OUT="backups/caskb-v${VER}-backup-$(date +%Y%m%d-%H%M%S).sql"
pg_dump "$DSN" > "$OUT"
echo "备份完成: $OUT ($(wc -c < "$OUT" | tr -d ' ') bytes)"
command -v shasum >/dev/null && shasum -a 256 "$OUT"