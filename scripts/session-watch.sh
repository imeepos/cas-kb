#!/bin/bash
# cas-kb 会话心跳巡检:列活跃分支最近 commit 年龄,超死亡阈值标 STALE 并退出非零。
# 依据:docs/research/ops-patterns.md §1.3/§1.4(T57-B);纪律成文见 AGENTS.md「会话心跳纪律」。
# 判定归脚本,处置归负责人:本脚本不删分支、不合并、不改任何状态(死亡进终态记录,动刀由人)。
#
# 用法(零参可跑):
#   ./scripts/session-watch.sh                  # 巡检,死亡阈值 90 分钟
#   STALE_MIN=30 ./scripts/session-watch.sh     # 自定阈值(分钟)
#
# 范围:refs/heads 全部本地分支(worktree 间共享),排除 main(集成分支,非会话工作面)与
# 当前分支(正在写,自然新鲜)。健康:逐分支「OK <分支> <年龄>min」,退出 0;
# 任一超阈值:输出「STALE <分支> <年龄>min」+ 汇总行,退出 1(冻结名单处置见 AGENTS.md)。
set -euo pipefail

STALE_MIN="${STALE_MIN:-90}"
CUR="$(git branch --show-current)"
NOW="$(date +%s)"

stale=0
total=0
while read -r branch ts; do
  [ "$branch" = "main" ] && continue
  [ -n "$CUR" ] && [ "$branch" = "$CUR" ] && continue
  total=$((total + 1))
  age=$(( (NOW - ts) / 60 ))
  if [ "$age" -gt "$STALE_MIN" ]; then
    echo "STALE $branch ${age}min"
    stale=$((stale + 1))
  else
    echo "OK $branch ${age}min"
  fi
done < <(git for-each-ref refs/heads --format='%(refname:short) %(committerdate:unix)')

if [ "$stale" -gt 0 ]; then
  echo "心跳巡检:$stale/$total 条会话分支超 ${STALE_MIN}min 无 commit(进入冻结名单,处置见 AGENTS.md「会话心跳纪律」)"
  exit 1
fi
echo "心跳巡检:$total 条会话分支全部健康(阈值 ${STALE_MIN}min)"
