package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/store"
)

// cmdInit 初始化(迁移)本地数据库 schema。
func cmdInit(ctx context.Context, args []string) error {
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	name, target := store.DescribeBackend(effectiveDSN())
	fmt.Printf("已初始化 cas-kb schema(后端 %s:%s)\n", name, target)
	return nil
}
