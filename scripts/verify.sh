#!/bin/bash
# cas-kb 单一质量门禁:格式 → 构建 → vet → 单元测试 →(可选)集成测试。
# CI 只需调用本脚本;本地开发同样以它为准。
set -euo pipefail
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

if [ -n "${KB_TEST_DSN:-}" ]; then
  go test -count=1 ./...
else
  echo "KB_TEST_DSN 未设置,跳过集成测试"
fi
