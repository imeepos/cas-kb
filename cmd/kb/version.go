package main

import (
	"context"
	"fmt"
	"runtime"
)

// version 由发布流水线经 -ldflags "-X main.version=<tag>" 注入;
// 本地 go build 未注入时为 dev(kb update 对 dev 构建只展示最新版不比较)。
var version = "dev"

// cmdVersion 打印版本与构建平台信息。
func cmdVersion(_ context.Context, _ []string) error {
	fmt.Printf("kb %s(%s/%s,%s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	return nil
}
