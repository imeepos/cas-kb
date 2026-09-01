#!/bin/bash
# cas-kb 库恢复:把 backup.sh 的产物导入一个全新的目标库(先删后建,误操作面最小)。
# 用法:./scripts/restore.sh <backup.sql> <目标数据库名>
# 注意:备份属于旧 schema 时,需用对应版本的 kb 二进制访问;本脚本只负责导入,不做版本升级。
set -euo pipefail
cd "$(dirname "$0")/.."

for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin \
          /opt/homebrew/opt/postgresql@16/bin /opt/homebrew/opt/postgresql@17/bin /opt/homebrew/opt/postgresql@18/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH

[ $# -eq 2 ] || { echo "用法: $0 <backup.sql> <目标数据库名>"; exit 1; }
SQL="$1"
DB="$2"
[ -f "$SQL" ] || { echo "restore: 备份文件不存在: $SQL"; exit 1; }

BASE_DSN="${KB_DSN:-postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable}"
ADMIN_DSN="${BASE_DSN%/*}/postgres"
command -v psql >/dev/null || { echo "restore: 需要 psql"; exit 1; }

# 从文件名嗅探备份的 schema 版本(backup.sh 命名约定),内容兜底
BAK_VER=$(printf '%s' "$SQL" | grep -oE -- "-v[0-9]+-backup" | grep -oE "[0-9]+" | head -1 || true)
[ -n "$BAK_VER" ] || BAK_VER=$(grep -oE "'schema_version', *'[0-9]+'" "$SQL" | grep -oE "[0-9]+" | head -1 || true)

psql "$ADMIN_DSN" -q -c "DROP DATABASE IF EXISTS \"$DB\""
psql "$ADMIN_DSN" -q -c "CREATE DATABASE \"$DB\""
psql "${BASE_DSN%/*}/$DB" -q -f "$SQL"
echo "恢复完成: $DB ($(wc -c < "$SQL" | tr -d ' ') bytes)"
if [ -n "${BAK_VER:-}" ] && [ "$BAK_VER" != "4" ]; then
  echo "提醒: 该备份属于 schema v$BAK_VER,v4 二进制会拒绝打开;请用对应版本的 kb 访问。"
fi