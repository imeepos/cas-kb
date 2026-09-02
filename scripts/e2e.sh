#!/bin/bash
# cas-kb 端到端验收:临时目录 + 临时库,跑完整生命周期;产物不入库。
# 用法:./scripts/e2e.sh [postgres://…]
#   无参数(默认)→ SQLite 后端(临时库文件,零外部依赖)
#   postgres://…   → PostgreSQL 后端(临时库;额外覆盖 pg_dump backup.sh/restore.sh)
# 也可经 KB_DSN 传入 postgres:// 启用 PG 模式。
set -euo pipefail
# Homebrew 工具(psql/pg_dump/go/gofmt)可能不在非交互 shell 的 PATH 里,逐个补齐
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
cd "$(dirname "$0")/.."
REPO="$(pwd)"

BASE_DSN="${1:-${KB_DSN:-}}"
MODE_SQLITE=1
case "$BASE_DSN" in
  postgres://*|postgresql://*) MODE_SQLITE=0 ;;
esac

WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
echo "== 构建 =="
go build -o "$WORK/kb" ./cmd/kb
KB="$WORK/kb"

if [ "$MODE_SQLITE" = 0 ]; then
  ADMIN_DSN="${BASE_DSN%/*}/postgres"
  E2E_DB="caskb_e2e_$(date +%s)_$$"
  E2E_DSN="${BASE_DSN%/*}/$E2E_DB"
  command -v psql >/dev/null || { echo "e2e(PG) 需要 psql"; exit 1; }
  echo "== 临时库 $E2E_DB(PostgreSQL)=="
  psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS $E2E_DB"
  psql "$ADMIN_DSN" -qAc "CREATE DATABASE $E2E_DB"
  cleanup() { psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS $E2E_DB" >/dev/null; rm -rf "$WORK"; }
  export KB_DSN="$E2E_DSN"
else
  echo "== 临时库文件(SQLite)=="
  export KB_DSN="sqlite:$WORK/e2e.db"
fi
cd "$WORK"
step() { echo "--- $1"; }
has() { echo "$2" | grep -qF "$1" || { echo "断言失败: 期望包含 [$1],实际: $2"; exit 1; }; }

step "version";              has "kb " "$($KB version)"
step "init";                 $KB init > /dev/null
step "init 显示后端";         out=$($KB init); if [ "$MODE_SQLITE" = 0 ]; then has "postgres" "$out"; else has "sqlite" "$out"; fi
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
# ---- M3.8 目录层级 ----
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

# ---- M3.9 库级运维(原生 .ckb,跨后端)----
step "kb backup";            BKF=$($KB backup | grep -oE '[^ ]+\.ckb' | head -1)
step "wipe 预览不动数据";     out=$($KB wipe); has "将清空" "$out"
if ! echo "$($KB -p alpha note ls)" | grep -qF "A1"; then echo "断言失败: 无 --force 的 wipe 不得清库"; exit 1; fi
step "kb wipe --force";      $KB wipe --force > /dev/null
step "清空后为空";           has "(no notes)" "$($KB -p alpha note ls)"
step "kb restore";           $KB restore "$BKF" > /dev/null
step "恢复读回";             has "A1" "$($KB -p alpha note ls)"; has "go/" "$($KB -p alpha dir tree)"
step "清理 .ckb";            rm -f "$BKF"

# ---- 跨版本恢复(升级演练结论:header ∈ [4, 当前] 可恢复)----
step "跨版本备份(header 改写为 v4)"; CVB=$($KB backup | grep -oE '[^ ]+\.ckb' | head -1)
sed 's/"schema_version":5,/"schema_version":4,/' "$CVB" > cv4.ckb
step "v4 备份恢复成功并提示升级"; out=$($KB restore cv4.ckb --force); has "恢复完成" "$out"; has "schema v4" "$out"
step "跨版本恢复后数据可读";   has "A1" "$($KB -p alpha note ls)"
step "清理跨版本产物";         rm -f cv4.ckb "$CVB"

# ---- PostgreSQL 专属:pg_dump 全保真备份/恢复 ----
if [ "$MODE_SQLITE" = 0 ]; then
  step "backup.sh";          BKF=$(bash "$REPO/scripts/backup.sh" "$E2E_DSN" | grep -oE 'backups/[^ ]+' | head -1)
  step "restore.sh";         bash "$REPO/scripts/restore.sh" "$BKF" "${E2E_DB}_r" > /dev/null
  step "恢复库读回";         has "A1" "$(KB_DSN="${BASE_DSN%/*}/${E2E_DB}_r" $KB -p alpha note ls)"
  step "清理恢复库与备份";   psql "$ADMIN_DSN" -qAc "DROP DATABASE IF EXISTS ${E2E_DB}_r" > /dev/null; rm -f "$REPO/$BKF"
fi

# ---- M4 检索与链接解析(DESIGN §7/§3.3)----
step "M4 建检索语料";         $KB -p alpha note set go/searching/index --title "检索索引" --body "倒排与 BM25" -m m4 > /dev/null
step "search 命中";          out=$($KB -p alpha search BM25); has "go/searching/index" "$out"
step "search 确定性(可复现)"; diff <($KB -p alpha search BM25) <($KB -p alpha search BM25) || { echo "断言失败: 同快照搜索应逐字节一致"; exit 1; }
step "search --json 契约";   jout=$($KB -p alpha search BM25 --json); has '"path": "go/searching/index"' "$jout"
step "search --at 历史快照";  has "(no results)" "$($KB -p alpha search BM25 --at "$S1")"
step "search 无命中";        has "(no results)" "$($KB -p alpha search 绝无仅有词q)"
step "link resolve 全路径";   out=$($KB -p alpha link resolve go/searching/index); has "path:  go/searching/index" "$out"
step "link resolve 叶名回退"; out=$($KB -p alpha link resolve task); has "path:  task" "$out"
step "index rebuild";        out=$($KB -p alpha index rebuild); has "index sha256:" "$out"
step "rebuild 后检索可用";    out=$($KB -p alpha search BM25); has "go/searching/index" "$out"
step "fsck 通过(索引可达)";   has "完整,无问题" "$($KB fsck)"

# ---- 批量导入(压测根治:单快照+一次索引增量)----
step "生成 bulk 语料";        for i in $(seq 1 50); do printf '{"path":"b%d/n%d","title":"B%d channel","tags":["bulk"],"body":"第 %d 条 bulk channel 内容"}\n' "$((i%4))" "$i" "$i" "$i"; done > bulk.jsonl
step "bulk import 50 条";     out=$($KB -p alpha bulk import bulk.jsonl -m "bulk 50"); has "bulk import 50 条" "$out"
step "bulk 后检索命中";        out=$($KB -p alpha search channel -n 5); has "channel" "$out"
step "bulk 后条目数";         n=$($KB -p alpha note ls | wc -l | tr -d ' '); [ "$n" -ge 51 ] || { echo "断言失败: 条目数 $n < 51"; exit 1; }
step "清理 bulk 语料";        rm -f bulk.jsonl

# ---- 暂存工作流(stage→commit,借鉴 git)----
step "stage 两条";           $KB -p alpha note set st/a --title SA --body sa --stage >/dev/null; $KB -p alpha note set st/b --title SB --body sb --stage >/dev/null
step "暂存对 main 不可见";     if echo "$($KB -p alpha note ls)" | grep -qF "st/a"; then echo "断言失败: 暂存不应出现在 main"; exit 1; fi
step "stage status";         out=$($KB -p alpha stage); has "A  st/a" "$out"; has "A  st/b" "$out"
step "commit 暂存";          out=$($KB -p alpha commit -m "stage commit"); has "已提交 2 处变更" "$out"
step "提交后可见可检索";       has "SA" "$($KB -p alpha note get st/a)"; has "st/a" "$($KB -p alpha search sa | head -1)"
step "再次 commit 无暂存";     has "(no staged changes)" "$($KB -p alpha commit)"
step "abort 丢弃暂存";        $KB -p alpha note set st/z --title Z --body z --stage >/dev/null; out=$($KB -p alpha commit --abort); has "暂存已丢弃" "$out"
if echo "$($KB -p alpha note ls)" | grep -qF "st/z"; then echo "断言失败: abort 后不应有 st/z"; exit 1; fi

# ---- Markdown 互操作(export md / import md,DESIGN §6.9)----
step "project create mdio";     $KB project create mdio > /dev/null
step "markdown 源目录";          mkdir -p mds/go
cat > mds/go/channel.md <<'MD'
---
title: 通道笔记
tags: go, 并发
---
chan 语义正文。
MD
cat > mds/idea.md <<'MD'
---
title: 点子
---
随手记的点子。
MD
step "import md 2 条";          out=$($KB -p mdio import md mds -m "md import"); has "import md 2 条" "$out"
step "导入后检索命中";           out=$($KB -p mdio search 通道); has "go/channel" "$out"
step "export md";               out=$($KB -p mdio export md mdout); has "export md 2 条" "$out"
step "roundtrip 逐字节一致";     diff -r mds mdout > /dev/null || { echo "断言失败: export(import(X)) 应与 X 逐字节一致"; exit 1; }
step "重复导出拒绝提示 --force";  if out=$($KB -p mdio export md mdout 2>&1); then echo "断言失败: 目标已存在应整批拒绝"; exit 1; else has "force 整批覆盖" "$out"; fi
step "改库后再导入还原";         $KB -p mdio note set idea --title 点子改 --body 改动 -m tweak > /dev/null
out=$($KB -p mdio import md mdout -m restore); has "import md 1 条" "$out"; has "点子" "$($KB -p mdio note get idea)"
step "再次导入零变更";           out=$($KB -p mdio import md mdout); has "无新快照" "$out"
step "再次导出仍逐字节一致";     $KB -p mdio export md mdout2 > /dev/null; diff -r mdout mdout2 > /dev/null || { echo "断言失败: 二次导出应逐字节一致"; exit 1; }
step "问题文件整批拒绝";         mkdir -p badmd; printf -- '---\ntags: x\n---\nno title\n' > badmd/no-title.md; printf 'plain text\n' > badmd/readme.txt
if out=$($KB -p mdio import md badmd 2>&1); then echo "断言失败: 问题文件应拒绝"; exit 1; else has "no-title.md" "$out"; has "readme.txt" "$out"; fi
step "清理 markdown 产物";       rm -rf mds mdout mdout2 badmd

# ---- 历史保留(gc --keep-last)----
PREV=$($KB -p alpha log | head -1 | awk '{print $1}')
step "造 12 个提交";          for i in $(seq 1 12); do $KB -p alpha note set hist/n$i --title H$i --body "b$i" -m "h$i" >/dev/null; done
step "gc --keep-last 10";    out=$($KB -p alpha gc --keep-last 10); has "保留策略: 最近 10 个快照" "$out"
step "head 数据可读";         has "H12" "$($KB -p alpha note get hist/n12)"
step "head 检索可用";         out=$($KB -p alpha search H12 | head -1); has "hist/n12" "$out"
step "旧快照数据保留";         has "SA" "$($KB -p alpha note get st/a --at "$PREV")"
step "被精简快照检索友好报错";  out=$($KB -p alpha search H12 --at "$PREV" 2>&1) || true; has "已被 gc 精简" "$out"
step "fsck 水印豁免";         has "完整,无问题" "$($KB -p alpha fsck)"

# ---- 只读 HTTP API(kb serve)----
command -v curl >/dev/null || { echo "e2e(serve) 需要 curl"; exit 1; }
step "后台起 kb serve";        SERVE_LOG="$WORK/serve.log"; "$KB" -p alpha serve --addr 127.0.0.1:0 > "$SERVE_LOG" 2>&1 & SERVE_PID=$!
SERVE_URL=""
for _ in $(seq 1 50); do
  SERVE_URL=$(grep -m1 '^监听' "$SERVE_LOG" 2>/dev/null | awk '{print $NF}' || true)
  if [ -n "$SERVE_URL" ]; then break; fi
  sleep 0.1
done
[ -n "$SERVE_URL" ] || { echo "断言失败: serve 未在 5s 内就绪: $(cat "$SERVE_LOG")"; kill "$SERVE_PID" 2>/dev/null || true; exit 1; }
step "serve healthz 探活";     has '"ok": true' "$(curl -sf "$SERVE_URL/healthz")"
step "serve api note 读单条";  has '"title": "A1"' "$(curl -sf "$SERVE_URL/api/v1/note?path=task")"
step "serve api search 检索";  has '"path": "hist/n12"' "$(curl -sf -G "$SERVE_URL/api/v1/search" --data-urlencode 'q=H12')"
step "serve 只读纪律 POST 405"; code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$SERVE_URL/api/v1/note"); [ "$code" = "405" ] || { echo "断言失败: POST 应 405,得到 $code"; exit 1; }
step "kill serve 优雅退出";    kill "$SERVE_PID"; wait "$SERVE_PID" || { echo "断言失败: serve 应优雅退出(退出码 0): $(cat "$SERVE_LOG")"; exit 1; }

step "gc + fsck";            out=$($KB gc); has "已备份" "$out"; has "完整,无问题" "$($KB fsck)"
echo "E2E_GREEN"
