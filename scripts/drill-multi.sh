#!/bin/bash
# cas-kb 演练固化:多端互写(drill-multi)——T42 剧本(docs/review/drill-multi-cli.md)的可重复执行形态。
# 范式蓝本:docs/research/best-practices-adoption.md §3.3(git t/ 四件套:逐条断言带标题 /
# mktemp 临时目录隔离 / trap 清理 / TAP 式汇总);serve 侧剧本见 scripts/drill-serve.sh。
#
# 用法(零参可跑;参数全走环境变量):
#   ./scripts/drill-multi.sh                     # 跑全部腿
#   KB_BIN=/path/to/kb ./scripts/drill-multi.sh  # 指定被测二进制(缺省现场 go build)
#   DRILL_RUN=2 ./scripts/drill-multi.sh         # 只跑第 2 腿(对齐 git t/ --run;被过滤的腿不计 skip)
#   DRILL_KEEP=1 ./scripts/drill-multi.sh        # 结束后保留现场目录供诊断(对齐 git t/ -d)
#
# 腿清单(编号即 DRILL_RUN 选择值):
#   1 冷启动合并:两库各自 init 写入 → pull --merge --allow-unrelated → 双亲/检索/ff/no-op
#   2 真实冲突裁决:冲突清单 / main 指针不动 / --stage 升格 → --continue 双亲收束
#   3 冻结拒绝:合并中态直接写与 pull 一律响亮拒绝,读不受限
#   4 --abort 回滚:回到合并前,无中间态残留
#   5 backup→wipe→restore→fsck 往返(含双亲合并历史逐行还原)
#
# 输出:逐腿 ok N - <腿标题> / not ok N - <腿标题>(原因),末尾 PASS x / FAIL y (skipped z);
# FAIL>0 ⇒ 退出码 1。腿前置依赖缺失以退出码 125 上报,记 skip——跳过不进 PASS/FAIL 分母但
# 汇总行显示(对齐 git t/ prereq 语义,「跳过 ≠ 失败」;本脚本纯 kb 内闭环,正常无 skip)。
set -euo pipefail
# Homebrew 工具(go)可能不在非交互 shell 的 PATH 里,补齐(同 e2e.sh 范式)
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
REPO="$(cd "$(dirname "$0")/.." && pwd)"

# —— 临时目录隔离 + trap 清理(e2e.sh 现成范式)——
WORK="$(mktemp -d "${TMPDIR:-/tmp}/drill-multi.XXXXXX")"
cleanup() {
  if [ "${DRILL_KEEP:-0}" = "1" ]; then
    echo "DRILL_KEEP=1:现场保留于 $WORK(两库与二进制在内,诊断后手工清理)"
    return
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "== drill-multi:多端互写演练(T42 剧本固化)=="
echo "1..5"
if [ -n "${KB_BIN:-}" ]; then
  echo "KB_BIN=$KB_BIN(外部指定,跳过现场构建)"
else
  echo "== 现场构建 KB_BIN(go build)=="
  (cd "$REPO" && go build -o "$WORK/kb" ./cmd/kb)
  KB_BIN="$WORK/kb"
fi

PASS=0; FAIL=0; SKIP=0
LEG=""; TITLE=""  # 当前腿号/标题,供断言失败时定位

# —— 断言与腿骨架(与 drill-serve.sh 内联同款,暂不抽 lib,调研 §3.4)——
# need <子串> <实际输出>:包含断言,失败以原因中止本腿
need() {
  echo "$2" | grep -qF -e "$1" || { echo "断言失败(腿 $LEG $TITLE): 期望包含 [$1],实际输出:"; echo "$2"; exit 1; }
}
# failrun <期望子串> <命令…>:命令必须失败退出,且输出含期望子串
failrun() {
  local expect="$1"; shift
  local out
  if out="$("$@" 2>&1)"; then
    echo "断言失败(腿 $LEG $TITLE): 命令应失败退出却成功: $*"; exit 1
  fi
  need "$expect" "$out"
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

# setup_pair <目录> <库A> <库B>:两库各自 init + 建 alpha 项目 + A 写基点 + B pull 建共同基点
# (腿 2/3/4 的同源前置;随后的分叉与冲突制造留在各腿,断言跟剧本走)
setup_pair() {
  local dir="$1" la="$2" lb="$3"
  KB_DSN="sqlite:$dir/$la.db" "$KB_BIN" init >/dev/null
  KB_DSN="sqlite:$dir/$lb.db" "$KB_BIN" init >/dev/null
  KB_DSN="sqlite:$dir/$la.db" "$KB_BIN" -p alpha project create alpha >/dev/null
  KB_DSN="sqlite:$dir/$lb.db" "$KB_BIN" -p alpha project create alpha >/dev/null
  KB_DSN="sqlite:$dir/$la.db" "$KB_BIN" -p alpha note set base/first --title 基点 --body v1 -m base >/dev/null
  KB_DSN="sqlite:$dir/$lb.db" "$KB_BIN" -p alpha pull "sqlite:$dir/$la.db" >/dev/null
}

# —— 腿 1:冷启动合并(两库各自 init,无共同历史)——
leg1() {
  local dir="$WORK/leg1"; mkdir -p "$dir"
  ka() { KB_DSN="sqlite:$dir/a.db" "$KB_BIN" "$@"; }
  kb() { KB_DSN="sqlite:$dir/b.db" "$KB_BIN" "$@"; }
  ka init >/dev/null; kb init >/dev/null
  ka -p alpha project create alpha >/dev/null
  kb -p alpha project create alpha >/dev/null
  # 各自写入 → 各自形成独立历史(正常起点,不是错误)
  ka -p alpha note set go/channel --title 通道 --body "a 侧冷启动内容" -m c1 >/dev/null
  kb -p alpha note set python/decorator --title 装饰器 --body "b 侧冷启动内容" -m d1 >/dev/null
  # 无旗标 / 只给 --merge 均拒绝,文案分流指路 --allow-unrelated(T44 D2)
  failrun "无共同历史" ka -p alpha pull "sqlite:$dir/b.db"
  failrun "--allow-unrelated" ka -p alpha pull "sqlite:$dir/b.db" --merge
  # 空基线合并:零冲突落库 + 冷启动完成提示(T47-D)
  local ahead bhead out parents
  ahead="$(ka -p alpha log | head -1 | awk '{print $1}')"
  bhead="$(kb -p alpha log | head -1 | awk '{print $1}')"
  out="$(ka -p alpha pull "sqlite:$dir/b.db" --merge --allow-unrelated)"
  need "冲突 0 条" "$out"
  need "合并快照 sha256:" "$out"
  need "冷启动完成" "$out"
  parents="$(ka -p alpha log | head -1 | grep -oE 'parent=[^ ]+')"
  echo "$parents" | grep -qF "$ahead" || { echo "断言失败: 合并快照双亲应含 ours 头 $ahead(与 theirs 断言对称,T57-C): $parents"; exit 1; }
  echo "$parents" | grep -qF "$bhead" || { echo "断言失败: 合并快照双亲应含 theirs 头 $bhead: $parents"; exit 1; }
  echo "$parents" | grep -q "," || { echo "断言失败: 合并快照应双亲: $parents"; exit 1; }
  # 跨侧内容可检索(CJK 2-gram 取对侧标题词元)
  need "python/decorator" "$(ka -p alpha search 装饰 | head -1)"
  need "完整,无问题" "$(ka fsck)"
  # 对侧 ff 至合并快照;再拉幂等 no-op
  out="$(kb -p alpha pull "sqlite:$dir/a.db")"
  need "fast-forward" "$out"
  out="$(ka -p alpha pull "sqlite:$dir/b.db")"
  need "已是最新" "$out"
}

# —— 腿 2:真实冲突裁决(冲突清单/指针不动/--stage 升格/--continue 双亲)——
leg2() {
  local dir="$WORK/leg2"; mkdir -p "$dir"
  ka() { KB_DSN="sqlite:$dir/a.db" "$KB_BIN" "$@"; }
  kb() { KB_DSN="sqlite:$dir/b.db" "$KB_BIN" "$@"; }
  setup_pair "$dir" a b
  # 分叉:同路径双侧异改
  ka -p alpha note set shared/decision --title 决议 --body "a 侧版本" -m ours >/dev/null
  kb -p alpha note set shared/decision --title 决议 --body "b 侧版本" -m theirs >/dev/null
  local ahead out parents
  ahead="$(ka -p alpha log | head -1 | awk '{print $1}')"
  # 冲突检出:退出码非零 + 冲突清单(路径/类别/三侧)
  local cout
  if cout="$(ka -p alpha pull "sqlite:$dir/b.db" --merge 2>&1)"; then
    echo "断言失败: 冲突应退出码非零: $cout"; exit 1
  fi
  need "冲突 1 条" "$cout"
  need "shared/decision" "$cout"
  need "content" "$cout"
  need "main-merge" "$cout"
  # main 指针不动;中间态分支建立
  [ "$(ka -p alpha log | head -1 | awk '{print $1}')" = "$ahead" ] || { echo "断言失败: 冲突后 main 指针不应推进"; exit 1; }
  ka -p alpha branch ls | grep -qF "main-merge" || { echo "断言失败: 应存在 main-merge 中间态分支"; exit 1; }
  # --stage 升格为裁决动作
  out="$(ka -p alpha note set shared/decision --title 决议 --body "裁决稿" --stage -m 采用合并稿)"
  need "staged" "$out"
  out="$(ka -p alpha stage)"
  need "已裁决" "$out"
  # --continue 收束:双亲合并快照 + 中间态清理
  out="$(ka -p alpha merge --continue -m 裁决收束)"
  need "合并完成" "$out"
  need "1 条裁决" "$out"
  parents="$(ka -p alpha log | head -1 | grep -oE 'parent=[^ ]+')"
  echo "$parents" | grep -qF "$ahead" || { echo "断言失败: 收束快照双亲应含 ours 头 $ahead(与 theirs 断言对称,T57-C): $parents"; exit 1; }
  echo "$parents" | grep -q "," || { echo "断言失败: 收束快照应双亲: $parents"; exit 1; }
  need "裁决稿" "$(ka -p alpha note get shared/decision)"
  need "完整,无问题" "$(ka fsck)"
  if ka -p alpha branch ls | grep -qF "main-merge"; then echo "断言失败: 收束后不应残留 main-merge"; exit 1; fi
}

# —— 腿 3:冻结拒绝(合并中态直接写与 pull 响亮拒绝,读不受限)——
leg3() {
  local dir="$WORK/leg3"; mkdir -p "$dir"
  ka() { KB_DSN="sqlite:$dir/a.db" "$KB_BIN" "$@"; }
  kb() { KB_DSN="sqlite:$dir/b.db" "$KB_BIN" "$@"; }
  setup_pair "$dir" a b
  ka -p alpha note set shared/decision --title 决议 --body "a 侧版本" -m ours >/dev/null
  kb -p alpha note set shared/decision --title 决议 --body "b 侧版本" -m theirs >/dev/null
  # 进入合并中间态
  failrun "冲突 1 条" ka -p alpha pull "sqlite:$dir/b.db" --merge
  # 直接写拒绝(可行动文案)
  failrun "未完成合并" ka -p alpha note set shared/decision --title 绕过 --body zz -m z
  # 中间态下 pull 一并拒绝(会推进 ours 头,使裁决重放失去前提)
  failrun "未完成合并" ka -p alpha pull "sqlite:$dir/b.db"
  # 读不受限
  need "a 侧版本" "$(ka -p alpha note get shared/decision)"
  need "基点" "$(ka -p alpha note get base/first)"
}

# —— 腿 4:--abort 回滚(回到合并前,无中间态残留)——
leg4() {
  local dir="$WORK/leg4"; mkdir -p "$dir"
  ka() { KB_DSN="sqlite:$dir/a.db" "$KB_BIN" "$@"; }
  kb() { KB_DSN="sqlite:$dir/b.db" "$KB_BIN" "$@"; }
  setup_pair "$dir" a b
  ka -p alpha note set shared/decision --title 决议 --body "a 侧版本" -m ours >/dev/null
  kb -p alpha note set shared/decision --title 决议 --body "b 侧版本" -m theirs >/dev/null
  failrun "冲突 1 条" ka -p alpha pull "sqlite:$dir/b.db" --merge
  local ahead before_log out
  ahead="$(ka -p alpha log | head -1 | awk '{print $1}')"
  before_log="$(ka -p alpha log)"
  out="$(ka -p alpha merge --abort)"
  need "已放弃合并" "$out"
  if ka -p alpha branch ls | grep -qF "main-merge"; then echo "断言失败: abort 后不应残留 main-merge"; exit 1; fi
  [ "$(ka -p alpha log | head -1 | awk '{print $1}')" = "$ahead" ] || { echo "断言失败: abort 后 main 应回到合并前"; exit 1; }
  [ "$(ka -p alpha log)" = "$before_log" ] || { echo "断言失败: abort 后 log 应与合并前逐行一致"; exit 1; }
  need "a 侧版本" "$(ka -p alpha note get shared/decision)"
  need "完整,无问题" "$(ka fsck)"
  # 冻结解除:恢复正常写入
  ka -p alpha note set shared/decision --title 决议 --body "abort 后新写" -m after-abort >/dev/null
  need "abort 后新写" "$(ka -p alpha note get shared/decision)"
}

# —— 腿 5:backup→wipe→restore→fsck 往返(含双亲合并历史逐行还原)——
leg5() {
  local dir="$WORK/leg5"; mkdir -p "$dir"
  ka() { KB_DSN="sqlite:$dir/a.db" "$KB_BIN" "$@"; }
  kn() { KB_DSN="sqlite:$dir/n.db" "$KB_BIN" "$@"; }
  ka init >/dev/null
  ka -p alpha project create alpha --desc "演练恢复项目" >/dev/null
  ka -p alpha note set docs/a --title 甲 --body "内容 a" -m a >/dev/null
  ka -p alpha note set docs/b --title 乙 --body "内容 b" -m b >/dev/null
  # 历史里带一个双亲合并快照(冷启动对合并进来,T42 腿 3 同款形态)
  kn init >/dev/null
  kn -p alpha project create alpha >/dev/null
  kn -p alpha note set xside/n --title 丙 --body "n 侧独有" -m n1 >/dev/null
  local out
  out="$(ka -p alpha pull "sqlite:$dir/n.db" --merge --allow-unrelated)"
  need "冷启动完成" "$out"
  local before_notes before_log bk
  before_notes="$(ka -p alpha note ls)"
  before_log="$(ka -p alpha log)"
  echo "$before_log" | grep -q "," || { echo "断言失败: 备份前历史应含双亲合并快照"; exit 1; }
  bk="$dir/backup.ckb"
  out="$(ka backup "$bk")"
  need "备份完成" "$out"
  out="$(ka wipe --force)"
  need "已清空" "$out"
  need "(no notes)" "$(ka -p alpha note ls)"
  out="$(ka restore "$bk")"
  need "恢复完成" "$out"
  [ "$(ka -p alpha note ls)" = "$before_notes" ] || { echo "断言失败: 恢复后 note ls 应与恢复前一致"; exit 1; }
  [ "$(ka -p alpha log)" = "$before_log" ] || { echo "断言失败: 恢复后 log 应逐行还原(含双亲合并行)"; exit 1; }
  need "完整,无问题" "$(ka fsck)"
}

run_leg 1 "冷启动合并(空基线 --allow-unrelated:双亲/检索/ff/no-op)" leg1
run_leg 2 "真实冲突裁决(冲突清单/指针不动/--stage 升格/--continue 双亲)" leg2
run_leg 3 "冻结拒绝(合并中态直接写与 pull 响亮拒绝,读不受限)" leg3
run_leg 4 "--abort 回滚(回到合并前,无中间态残留)" leg4
run_leg 5 "backup→wipe→restore→fsck 往返(合并历史逐行还原)" leg5

echo
echo "PASS $PASS / FAIL $FAIL (skipped $SKIP)"
[ "$FAIL" -eq 0 ] || exit 1
