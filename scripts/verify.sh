#!/bin/bash
# cas-kb 单一质量门禁:格式 → 构建 → vet → 单元测试 →(可选)集成测试。
# CI 只需调用本脚本;本地开发同样以它为准。
set -euo pipefail
# Homebrew 工具(psql/pg_dump/go/gofmt)可能不在非交互 shell 的 PATH 里,逐个补齐
for _d in /opt/homebrew/bin /usr/local/bin /usr/local/go/bin; do
  [ -d "$_d" ] && PATH="$_d:$PATH"
done
export PATH
cd "$(dirname "$0")/.."

UNFMT=$(gofmt -l .)
if [ -n "$UNFMT" ]; then
  echo "以下文件未通过 gofmt(运行 gofmt -w 修复):"
  echo "$UNFMT"
  exit 1
fi

go build ./...
go vet ./...
go test ./...

# 端到端验收(SQLite 默认后端,自建临时库):补齐「单测全绿但链路断裂」的盲区
# (M4 复盘 P0:此前 verify 不跑 e2e,CLI 链路回归只能靠手工发现)
echo "== e2e(SQLite)=="
./scripts/e2e.sh

if [ -n "${KB_TEST_DSN:-}" ]; then
  go test -count=1 ./...
else
  echo "KB_TEST_DSN 未设置,跳过集成测试"
fi
