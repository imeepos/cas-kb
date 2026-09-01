package main

import (
	"context"
	"fmt"
)

// cmdInit 初始化(迁移)本地数据库 schema。
func cmdInit(ctx context.Context, args []string) error {
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Println("已初始化 cas-kb schema")
	return nil
}
