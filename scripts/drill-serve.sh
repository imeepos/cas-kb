#!/bin/bash
# cas-kb 演练固化:serve 运维(drill-serve)——T43 剧本(docs/review/drill-serve.md)的可重复执行形态。
# 准绳:docs/serve.md(端点契约权威为 DESIGN §8.5/§8.6);范式蓝本:docs/research/best-practices-adoption.md §3.3
# (git t/ 四件套:逐条断言带标题 / mktemp 临时目录隔离 / trap 清理 / TAP 式汇总)。
#
# 用法(零参可跑;参数全走环境变量):
#   ./scripts/drill-serve.sh                      # 全部腿,端口 127.0.0.1:18787
#   DRILL_PORT=18800 ./scripts/drill-serve.sh     # 换端口(占用时退出并提示)
#   KB_BIN=/path/to/kb ./scripts/drill-serve.sh   # 指定被测二进制(缺省现场 go build)
#   DRILL_RUN=2 ./scripts/drill-serve.sh          # 只跑第 2 腿(对齐 git t/ --run)
#   DRILL_KEEP=1 ./scripts/drill-serve.sh         # 结束后保留现场目录供诊断
#
# 腿清单(编号即 DRILL_RUN 选择值):
#   1 默认绑定与只读基线(横幅逐字/healthz/读端点/无令牌 403 文案/405;lsof 核验监听面,缺失即 skip)
#   2 令牌写入闭环(openssl 生成→600 环境文件→注入启动→201→写后立即可见→CLI 一致→DELETE;openssl 缺失即 skip)
#   3 鉴权矩阵(无头 401/错令牌 401 不回显/正确 201;读端点始终无鉴权)
#   4 锁忙 503 机制(sqlite3 外部持 BEGIN EXCLUSIVE 制造;机制可选,sqlite3 缺失即 skip)
#
# 生命周期:每个 serve 进程 PID 记入 $WORK/serve.pids,父进程 trap 统一清理(对齐 git t/test-lib.sh
# 的「清理永远挂在信号/退出钩子上」);正常路径腿内 SIGTERM 优雅退出并断言退出码 0。
# 输出:逐腿 ok N - <腿标题> / not ok N - <腿标题>(原因),末尾 PASS x / FAIL y (skipped z);
# FAIL>0 ⇒ 退出码 1;skip 不进 PASS/FAIL 分母但汇总行显示。
set -euo pipefail
# Homebrew 工具(go/lsof/openssl)可能不在非交互 shell 的 PATH 里,补齐(同 e2e.sh 范式)
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
REPO="$(cd "$(dirname "$0")/.." && pwd)"

PORT="${DRILL_PORT:-18787}"
BASE="127.0.0.1:$PORT"
URL="http://$BASE"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/drill-serve.XXXXXX")"
PIDS="$WORK/serve.pids"
cleanup() {
  # serve 进程清理挂 trap:先 TERM 后 KILL(残留实例兜底),再决定现场去留
  if [ -s "$PIDS" ]; then
    while read -r _p; do kill "$_p" 2>/dev/null || true; done < "$PIDS"
    sleep 1
    while read -r _p; do kill -9 "$_p" 2>/dev/null || true; done < "$PIDS"
  fi
  if [ "${DRILL_KEEP:-0}" = "1" ]; then
    echo "DRILL_KEEP=1:现场保留于 $WORK(serve 日志与库文件在内,诊断后手工清理)"
    return
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "== drill-serve:serve 运维演练(T43 剧本固化)=="
echo "1..4"
echo "端口 $BASE(DRILL_PORT 可改)"
command -v curl >/dev/null || { echo "drill-serve 需要 curl"; exit 1; }
if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "端口 $PORT 已被占用(可能是残留实例;DRILL_PORT 可改),退出"
  exit 1
fi
if [ -n "${KB_BIN:-}" ]; then
  echo "KB_BIN=$KB_BIN(外部指定,跳过现场构建)"
else
  echo "== 现场构建 KB_BIN(go build)=="
  (cd "$REPO" && go build -o "$WORK/kb" ./cmd/kb)
  KB_BIN="$WORK/kb"
fi

PASS=0; FAIL=0; SKIP=0
LEG=""; TITLE=""  # 当前腿号/标题,供断言失败时定位

# —— 断言与腿骨架(与 drill-multi.sh 内联同款,暂不抽 lib,调研 §3.4)——
# need <子串> <实际输出>:包含断言,失败以原因中止本腿
need() {
  echo "$2" | grep -qF -e "$1" || { echo "断言失败(腿 $LEG $TITLE): 期望包含 [$1],实际输出:"; echo "$2"; exit 1; }
}
# codeof <curl 参数…>:只取 HTTP 状态码
codeof() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
# http <方法> <curl 参数…>:curl 包装,输出 = 响应体 + 末行状态码(配 rc_of/body_of 取用)
http() { curl -s -w '\n%{http_code}' -X "$1" "${@:2}"; }
rc_of() { printf '%s\n' "$1" | tail -n 1; }
body_of() { printf '%s' "$1" | sed '$d'; }
# start_serve <日志> [ENV=值 …]:起 serve 于 $BASE,5s 内等横幅「监听」行;PID 写入全局 SERVE_PID,
# 失败打印日志并返回非零。注意:必须以普通调用使用(不要放进命令替换),否则 serve 会成为
# 子 shell 的孩子,腿内 stop_serve 的 wait 将够不着它
start_serve() {
  local log="$1"; shift
  env "$@" "$KB_BIN" serve --addr "$BASE" >"$log" 2>&1 &
  SERVE_PID=$!
  echo "$SERVE_PID" >> "$PIDS"
  local i
  for i in $(seq 1 50); do
    grep -qm1 '^监听' "$log" 2>/dev/null && return 0
    kill -0 "$SERVE_PID" 2>/dev/null || break
    sleep 0.1
  done
  echo "serve 未在 5s 内就绪,日志:"
  sed 's/^/    /' "$log"
  return 1
}
# stop_serve <pid>:SIGTERM 优雅退出并断言退出码 0(§1 排空承诺)
stop_serve() {
  local pid="$1"
  kill "$pid" 2>/dev/null || { echo "断言失败: serve 进程已提前退出"; return 1; }
  wait "$pid" 2>/dev/null || { echo "断言失败: serve 应优雅退出(退出码 0)"; return 1; }
}
# initdb <目录>:建临时库 + alpha 项目(serve 的 -p 作用域)
initdb() {
  local dir="$1"
  KB_DSN="sqlite:$dir/ops.db" "$KB_BIN" init >/dev/null
  KB_DSN="sqlite:$dir/ops.db" "$KB_BIN" -p alpha project create alpha >/dev/null
}
# run_leg <腿号> <标题> <腿函数>:子 shell 承载(保留 set -e 语义),失败打印 TAP 行与日志尾部
run_leg() {
  LEG="$1"; TITLE="$2"
  if [ -n "${DRILL_RUN:-}" ] && [ "${DRILL_RUN}" != "$1" ]; then return 0; fi
  echo "--- 腿 $1:$2"
  local log="$WORK/leg-$1.log" st=0
  ( "$3" ) >"$log" 2>&1 || st=$?
  if [ "$st" -eq 0 ]; then
    echo "ok $1 - $2"
    PASS=$((PASS+1))
  elif [ "$st" -eq 125 ]; then
    echo "ok $1 - $2 # skip $(tail -n 1 "$log")"
    SKIP=$((SKIP+1))
  else
    echo "not ok $1 - $2 (退出码 $st:$(tail -n 1 "$log"))"
    sed 's/^/    /' "$log" | tail -n 20
    FAIL=$((FAIL+1))
  fi
}

# —— 腿 1:默认绑定与只读基线 ——
leg1() {
  command -v lsof >/dev/null 2>&1 || { echo "lsof 缺失:默认绑定监听面无法核验"; exit 125; }
  local dir="$WORK/leg1"; mkdir -p "$dir"
  initdb "$dir"
  local log="$dir/serve.log" first lsout
  start_serve "$log" "KB_DSN=sqlite:$dir/ops.db" "KB_PROJECT=alpha" || exit 1
  # 横幅自检文案逐字(§2.4:巡检时核对模式声明)
  first="$(head -n 1 "$log")"
  [ "$first" = "kb serve 只读 HTTP API(未配置写入令牌,纯只读)" ] || { echo "断言失败: 横幅首行与只读自检文案不符: $first"; exit 1; }
  grep -qF "监听 $URL" "$log" || { echo "断言失败: 横幅监听行不符: $(grep '^监听' "$log")"; exit 1; }
  # 只读基线:读端点无鉴权照常可用;空库 note 404
  [ "$(codeof "$URL/healthz")" = "200" ] || { echo "断言失败: healthz 应 200"; exit 1; }
  [ "$(codeof "$URL/api/v1/projects")" = "200" ] || { echo "断言失败: projects 应 200"; exit 1; }
  [ "$(codeof "$URL/api/v1/note?path=hello")" = "404" ] || { echo "断言失败: 空库 note 应 404"; exit 1; }
  # 未配置令牌:写端点一律 403(纯只读降级,§2.4 第 1 行文案)
  [ "$(codeof -X POST "$URL/api/v1/note")" = "403" ] || { echo "断言失败: 无令牌 POST 应 403"; exit 1; }
  need "服务未配置写入令牌" "$(curl -s -X POST "$URL/api/v1/note")"
  [ "$(codeof -X DELETE "$URL/api/v1/note?path=x")" = "403" ] || { echo "断言失败: 无令牌 DELETE 应 403"; exit 1; }
  # 只读 API 拒绝 POST → 405
  [ "$(codeof -X POST "$URL/api/v1/search")" = "405" ] || { echo "断言失败: POST 到只读端点应 405"; exit 1; }
  # 监听面核验(§1 巡检锚点):仅回环,无 0.0.0.0/*/:: 形态
  lsout="$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN)"
  echo "$lsout" | grep -qF "$BASE" || { echo "断言失败: lsof 应见 $BASE LISTEN: $lsout"; exit 1; }
  if echo "$lsout" | grep -qE '0\.0\.0\.0|\*:|\[::\]'; then echo "断言失败: 不应出现非回环监听: $lsout"; exit 1; fi
  stop_serve "$SERVE_PID"
}

# —— 腿 2:令牌写入闭环(§2.1 + §2.4 第 2–4 行)——
leg2() {
  command -v openssl >/dev/null 2>&1 || { echo "openssl 缺失:令牌无法按 §2.1 生成"; exit 125; }
  local dir="$WORK/leg2"; mkdir -p "$dir"
  initdb "$dir"
  # §2.1 生成与存放:openssl rand -hex 32 → 600 环境文件(不入版本库)
  local tok envf perm log resp rc body short logid
  tok="$(openssl rand -hex 32)"
  [ "${#tok}" -eq 64 ] || { echo "断言失败: openssl rand -hex 32 应输出 64 个 hex 字符"; exit 1; }
  envf="$dir/serve.env"
  ( umask 077 && printf 'KB_SERVE_TOKEN=%s\n' "$tok" > "$envf" )
  perm="$(stat -f '%Lp' "$envf" 2>/dev/null || stat -c '%a' "$envf")"
  [ "$perm" = "600" ] || { echo "断言失败: 环境文件权限应 600,得 $perm"; exit 1; }
  # 环境变量注入启动(§2.1 推荐方式,非 --token 旗标)
  log="$dir/serve.log"
  # shellcheck disable=SC1090
  set -a; . "$envf"; set +a
  start_serve "$log" "KB_DSN=sqlite:$dir/ops.db" "KB_PROJECT=alpha" "KB_SERVE_TOKEN=$KB_SERVE_TOKEN" || exit 1
  local first; first="$(head -n 1 "$log")"
  [ "$first" = "kb serve 写入型 HTTP API(已配置写入令牌,写端点需 Bearer 鉴权)" ] || { echo "断言失败: 横幅首行应为写入型声明: $first"; exit 1; }
  # 写入闭环:正确 Bearer POST → 201 + {path,address,short}
  resp="$(http POST -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -d '{"path":"demo/hello","title":"演练","tags":["drill"],"body":"serve 写入正文 wmark"}' "$URL/api/v1/note")"
  rc="$(rc_of "$resp")"; body="$(body_of "$resp")"
  [ "$rc" = "201" ] || { echo "断言失败: 正确令牌 POST 应 201,得 $rc: $body"; exit 1; }
  need '"path": "demo/hello"' "$body"
  need '"address"' "$body"
  short="$(printf '%s' "$body" | grep -oE '"short": "[^"]+"' | sed 's/.*: "//; s/"$//')"
  [ -n "$short" ] || { echo "断言失败: POST 响应应含 short 字段: $body"; exit 1; }
  # 写后立即可见 + CLI 读一致(§3「不存在第二套写行为」的读侧证据)
  [ "$(codeof "$URL/api/v1/note?path=demo/hello")" = "200" ] || { echo "断言失败: 写后 GET 应 200"; exit 1; }
  need '"title": "演练"' "$(curl -s "$URL/api/v1/note?path=demo/hello")"
  need "demo/hello" "$(curl -s -G "$URL/api/v1/search" --data-urlencode 'q=wmark')"
  logid="$(KB_DSN="sqlite:$dir/ops.db" "$KB_BIN" -p alpha log | head -1 | awk '{print $1}')"
  [ "$logid" = "$short" ] || { echo "断言失败: kb log 首列应等于 POST 响应 short: $logid vs $short"; exit 1; }
  need "演练" "$(KB_DSN="sqlite:$dir/ops.db" "$KB_BIN" -p alpha note get demo/hello)"
  need "完整,无问题" "$(KB_DSN="sqlite:$dir/ops.db" "$KB_BIN" fsck)"
  # 附加闭环:正确令牌 DELETE → 200;再 GET 404
  resp="$(http DELETE -H "Authorization: Bearer $tok" "$URL/api/v1/note?path=demo/hello")"
  rc="$(rc_of "$resp")"
  [ "$rc" = "200" ] || { echo "断言失败: DELETE 应 200,得 $rc: $resp"; exit 1; }
  [ "$(codeof "$URL/api/v1/note?path=demo/hello")" = "404" ] || { echo "断言失败: 删除后 GET 应 404"; exit 1; }
  stop_serve "$SERVE_PID"
}

# —— 腿 3:鉴权矩阵(无头 401 / 错令牌 401 不回显 / 正确 201;读端点始终无鉴权)——
leg3() {
  local dir="$WORK/leg3"; mkdir -p "$dir"
  initdb "$dir"
  local tok="drill-matrix-fixed-token"
  local log="$dir/serve.log" resp rc body
  start_serve "$log" "KB_DSN=sqlite:$dir/ops.db" "KB_PROJECT=alpha" "KB_SERVE_TOKEN=$tok" || exit 1
  # 无头 POST → 401(§2.4 第 2 行)
  resp="$(http POST -H 'Content-Type: application/json' -d '{"path":"api/n1","title":"T","body":"b"}' "$URL/api/v1/note")"
  rc="$(rc_of "$resp")"; body="$(body_of "$resp")"
  [ "$rc" = "401" ] || { echo "断言失败: 无头 POST 应 401,得 $rc"; exit 1; }
  need "缺少写入令牌" "$body"
  # 错令牌 → 401 且不回显真令牌(§2.4 第 3 行)
  resp="$(http POST -H 'Authorization: Bearer deadbeef' -H 'Content-Type: application/json' -d '{"path":"api/n1","title":"T","body":"b"}' "$URL/api/v1/note")"
  rc="$(rc_of "$resp")"; body="$(body_of "$resp")"
  [ "$rc" = "401" ] || { echo "断言失败: 错令牌 POST 应 401,得 $rc"; exit 1; }
  need "写入令牌无效" "$body"
  if printf '%s' "$body" | grep -qF "$tok"; then echo "断言失败: 401 响应不应回显令牌"; exit 1; fi
  # 正确令牌 → 201(§2.4 第 4 行)
  resp="$(http POST -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -d '{"path":"api/n1","title":"T","body":"b"}' "$URL/api/v1/note")"
  rc="$(rc_of "$resp")"
  [ "$rc" = "201" ] || { echo "断言失败: 正确令牌 POST 应 201,得 $rc: $resp"; exit 1; }
  # 配置令牌后读端点保持无鉴权(§2.4)
  [ "$(codeof "$URL/healthz")" = "200" ] || { echo "断言失败: 配置令牌后 healthz 应无鉴权 200"; exit 1; }
  [ "$(codeof "$URL/api/v1/tree")" = "200" ] || { echo "断言失败: 配置令牌后 tree 应无鉴权 200"; exit 1; }
  stop_serve "$SERVE_PID"
}

# —— 腿 4:锁忙 503 机制(机制可选:外部持 BEGIN EXCLUSIVE 制造,sqlite3 缺失即 skip)——
leg4() {
  command -v sqlite3 >/dev/null 2>&1 || { echo "sqlite3 缺失:无法外部持锁制造锁忙"; exit 125; }
  local dir="$WORK/leg4"; mkdir -p "$dir"
  initdb "$dir"
  local db="$dir/ops.db" tok="drill-503-token"
  local log="$dir/serve.log" resp rc
  start_serve "$log" "KB_DSN=sqlite:$db" "KB_PROJECT=alpha" "KB_SERVE_TOKEN=$tok" || exit 1
  local body='{"path":"api/n503","title":"T","body":"b"}'
  # 外部进程持 BEGIN EXCLUSIVE 15s(管道喂 sqlite3;进程退出未 COMMIT 即回滚释放)
  ( echo "BEGIN EXCLUSIVE;"; sleep 15; echo "COMMIT;" ) | sqlite3 "$db" &
  LOCK_PID=$!  # 全局:leg 返回后 EXIT trap 仍要能读到
  trap 'kill "${LOCK_PID:-}" 2>/dev/null || true' EXIT
  sleep 1  # 等外部锁生效
  # 锁忙窗口:正确令牌 POST 阻塞至 busy_timeout(约 10s)后 503;读端点不受影响(§2.4/§3)
  http POST -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -d "$body" "$URL/api/v1/note" > "$dir/503.resp" &
  local postpid=$!
  sleep 1
  [ "$(codeof "$URL/healthz")" = "200" ] || { echo "断言失败: 持锁期间读端点不应受影响"; exit 1; }
  wait "$postpid" || true
  resp="$(cat "$dir/503.resp")"
  rc="$(rc_of "$resp")"
  [ "$rc" = "503" ] || { echo "断言失败: 外部持锁时写应 503,得 $rc: $resp"; exit 1; }
  need "正被其他写入占用" "$resp"
  wait "$LOCK_PID" 2>/dev/null || true
  # 释放后重试同一 POST → 201(503 是可重试信号,写路径对象幂等)
  resp="$(http POST -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -d "$body" "$URL/api/v1/note")"
  rc="$(rc_of "$resp")"
  [ "$rc" = "201" ] || { echo "断言失败: 锁释放后重试应 201,得 $rc: $resp"; exit 1; }
  need "完整,无问题" "$(KB_DSN="sqlite:$db" "$KB_BIN" fsck)"
  stop_serve "$SERVE_PID"
}

run_leg 1 "默认绑定与只读基线(横幅逐字/403 文案/405;lsof 监听面)" leg1
run_leg 2 "令牌写入闭环(openssl→600 环境文件→201→写后立即可见→CLI 一致→DELETE)" leg2
run_leg 3 "鉴权矩阵(无头 401/错令牌 401 不回显/正确 201;读端点无鉴权)" leg3
run_leg 4 "锁忙 503 机制(外部持 BEGIN EXCLUSIVE;503 可重试闭环)" leg4

echo
echo "PASS $PASS / FAIL $FAIL (skipped $SKIP)"
[ "$FAIL" -eq 0 ] || exit 1
