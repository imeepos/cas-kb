#!/bin/bash
# cas-kb 端到端验收:临时目录 + 临时库,跑完整生命周期;产物不入库。
# 用法:./scripts/e2e.sh [基库 DSN](默认取 KB_DSN,再退回本地 5432/caskb)
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_DSN="${1:-${KB_DSN:-postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable}}"
ADMIN_DSN="${BASE_DSN%/*}/postgres"
E2E_DB="caskb_e2e_$(date +%s)_$$"
E2E_DSN="${BASE_DSN%/*}/$E2E_DB"
command -v psql >/dev/null || { echo "e2e 需要 psql"; exit 1; }
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
echo "== 构建 =="
go build -o "$WORK/kb" ./cmd/kb
echo "== 临时库 $E2E_DB =="
psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS $E2E_DB"
psql "$ADMIN_DSN" -qAc "CREATE DATABASE $E2E_DB"
export KB_DSN="$E2E_DSN"
cd "$WORK"
KB="$WORK/kb"
step() { echo "--- $1"; }
has() { echo "$2" | grep -qF "$1" || { echo "断言失败: 期望包含 [$1],实际: $2"; exit 1; }; }
step "init";                 $KB init > /dev/null
step "project create alpha"; $KB project create alpha > /dev/null
step "-p alpha note set A1"; $KB -p alpha note set task --title A1 --body v1 -m add1 > /dev/null
S1=$($KB -p alpha log | tail -1 | awk '{print $1}')
step "-p alpha note set A2"; $KB -p alpha note set task --title A2 --body v2 -m add2 > /dev/null
step "note get 当前为 A2";    has "A2" "$($KB -p alpha note get task)"
step "note get --at 读回 A1"; has "A1" "$($KB -p alpha note get task --at "$S1")"
step "diff S1->HEAD";        out=$($KB -p alpha diff "$S1" "$($KB -p alpha log | head -1 | awk '{print $1}')"); has "task" "$out"
step "reset 到 S1";          out=$($KB -p alpha reset "$S1"); has "放弃 1 个提交" "$out"
step "ls 只剩 A1";           has "A1" "$($KB -p alpha note ls)"
step "default 项目为空";      has "(no notes)" "$($KB note ls)"
step "project ls 统计";      has "alpha" "$($KB project ls)"; has "default" "$($KB project ls)"
step "gc + fsck";            out=$($KB gc); has "已备份" "$out"; has "完整,无问题" "$($KB fsck)"
psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS $E2E_DB" > /dev/null
echo "E2E_GREEN"
