#!/bin/bash
# cas-kb 端到端验收:临时目录 + 临时库,跑完整生命周期;产物不入库。
# 用法:./scripts/e2e.sh [基库 DSN](默认取 KB_DSN,再退回本地 5432/caskb)
set -euo pipefail
# Homebrew 工具(psql/pg_dump/go/gofmt)可能不在非交互 shell 的 PATH 里,逐个补齐
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
cd "$(dirname "$0")/.."
REPO="$(pwd)"
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
step "version";              has "kb " "$($KB version)"
step "init";                 $KB init > /dev/null
step "project create alpha"; $KB project create alpha --desc "e2e 演练项目" > /dev/null
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
step "project desc 读回";    has "e2e 演练项目" "$($KB project desc alpha)"
step "project ls --json";    jout=$($KB project ls --json); has '"description": "e2e 演练项目"' "$jout"
step "branch desc 设置";     $KB -p alpha branch desc main 工作线 > /dev/null
step "branch ls --json";     bout=$($KB -p alpha branch ls --json); has '"description": "工作线"' "$bout"
step "note ls --json 摘要";  has '"summary": "v1"' "$($KB -p alpha note ls --json)"
# ---- M3.8 目录层级(schema v4)----
step "dir add go";           $KB -p alpha dir add go > /dev/null
D0=$($KB -p alpha log | head -1 | awk '{print $1}')
step "嵌套 note set";        $KB -p alpha note set go/concurrency/channel --title Chan --body cs -m add-chan > /dev/null
step "note get 全路径";      has "path:  go/concurrency/channel" "$($KB -p alpha note get go/concurrency/channel)"
step "dir ls 子目录";        has "dir  concurrency" "$($KB -p alpha dir ls go)"
step "note ls --dir 递归";    has "channel" "$($KB -p alpha note ls --dir go/concurrency)"
step "dir tree 层级";        out=$($KB -p alpha dir tree); has "go/" "$out"; has "channel  Chan" "$out"
step "diff 全路径";          has "A  go/concurrency/channel" "$($KB -p alpha diff "$D0" main)"
if $KB -p alpha dir rm go >/dev/null 2>&1; then echo "断言失败: 非空目录应拒绝删除"; exit 1; fi
step "dir rm --force";       $KB -p alpha dir rm go --force > /dev/null
if echo "$($KB -p alpha note ls)" | grep -qF "channel"; then echo "断言失败: force 删除后不应再有条目"; exit 1; fi
step "根目录仍存 A1";        has "A1" "$($KB -p alpha note ls)"
step "dir add 幂等";         $KB -p alpha dir add go > /dev/null
step "backup.sh";            BKF=$(bash "$REPO/scripts/backup.sh" "$E2E_DSN" | grep -oE 'backups/[^ ]+' | head -1)
step "restore.sh";           bash "$REPO/scripts/restore.sh" "$BKF" "${E2E_DB}_r" > /dev/null
step "恢复库读回";           has "A1" "$(KB_DSN="${BASE_DSN%/*}/${E2E_DB}_r" $KB -p alpha note ls)"
step "清理恢复库与备份";     psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS ${E2E_DB}_r" > /dev/null; rm -f "$REPO/$BKF"
step "kb backup";            BKF=$($KB backup | grep -oE '[^ ]+\.ckb' | head -1)
step "kb wipe 预览不执行";   out=$($KB wipe); has "将清空" "$out"; has "A1" "$($KB -p alpha note ls)"
step "kb wipe --force";      $KB wipe --force > /dev/null
step "清空后为空";           has "(no notes)" "$($KB -p alpha note ls)"
step "kb restore";           $KB restore "$BKF" > /dev/null
step "恢复读回";            has "A1" "$($KB -p alpha note ls)"; has "task" "$($KB -p alpha note ls)"
step "清理 .ckb";            rm -f "$BKF"
step "gc + fsck";            out=$($KB gc); has "已备份" "$out"; has "完整,无问题" "$($KB fsck)"
psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS $E2E_DB" > /dev/null
echo "E2E_GREEN"
