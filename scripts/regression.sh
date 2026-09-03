#!/bin/bash
# cas-kb 季度/发版前回归聚合器:verify → drill-multi → drill-serve,逐段 ok/not ok,任一段失败整体退出非零。
# 依据:docs/research/ops-patterns.md §3.3/§3.4(T57-E);调度(launchd 季度任务
# docs/launchd/com.caskb.regression.plist)与归档约定见 README「开发与测试」。
#
# 用法(零参可跑):
#   ./scripts/regression.sh    # verify.sh(默认不含 drill)→ drill-multi.sh → drill-serve.sh
#
# 细节:KB_BIN 一次现场构建、两 drill 复用(照 drill 脚本的 KB_BIN 约定,省一次 go build);
# 全部输出 tee 到 /tmp/regression-<时间戳>.log(现场日志,不入 git;结论报告另行归档 docs/review/,
# 报告讲结论、日志留现场,与 e2e「产物不入库」纪律同构)。
set -uo pipefail
# Homebrew 工具(go)可能不在非交互 shell 的 PATH 里,补齐(同 verify.sh 范式)
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

LOG="/tmp/regression-$(date +%Y%m%d-%H%M%S).log"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/regression.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

FAIL=0
# seg <段名> <命令…>:跑一段,输出 TAP 行「ok/not ok <段名>」;失败不中止(后续段照跑),累计进 FAIL
seg() {
  local name="$1"; shift
  local rc=0
  "$@" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "ok $name"
  else
    echo "not ok $name(退出码 $rc)"
    FAIL=$((FAIL + 1))
  fi
}

# 段序列:门禁 → 两个演练;跑在管道里(tee 全量留档),末行汇总,FAIL>0 以非零退出
run() {
  echo "== regression:verify → drill-multi → drill-serve(T57-E 聚合;日志 tee 到 $LOG)=="
  echo "1..3"
  seg verify ./scripts/verify.sh
  echo "== 现场构建 KB_BIN(go build,两 drill 复用)=="
  if ! go build -o "$WORK/kb" ./cmd/kb; then
    echo "not ok drill-multi(前置 KB_BIN 构建失败)"
    echo "not ok drill-serve(前置 KB_BIN 构建失败)"
    FAIL=$((FAIL + 2))
    echo
    echo "PASS $((3 - FAIL)) / FAIL $FAIL"
    return 1
  fi
  seg drill-multi env KB_BIN="$WORK/kb" ./scripts/drill-multi.sh
  seg drill-serve env KB_BIN="$WORK/kb" ./scripts/drill-serve.sh
  echo
  echo "PASS $((3 - FAIL)) / FAIL $FAIL"
  [ "$FAIL" -eq 0 ]
}

set -o pipefail
run 2>&1 | tee "$LOG"
RC=$?
echo "日志:$LOG(现场全文,不入 git;报告归档约定见 README「开发与测试」)"
exit "$RC"
