package main

import (
	"context"
	"fmt"

	"github.com/imeepos/cas-kb/internal/embed"
)

// cmdIndex 处理 kb index 子命令(目前仅 rebuild)。
// --embed:在 BM25 索引之外全量重建语义向量索引(M6-A);
// 嵌入服务未配置(KB_EMBED_MODEL 未设置)时 embed.ProviderFromEnv 给可行动报错。
func cmdIndex(ctx context.Context, args []string) error {
	if len(args) < 1 || args[0] != "rebuild" {
		return fmt.Errorf("用法: kb index rebuild [-m msg] [--embed]")
	}
	f, err := parseFlags(args[1:], map[string]bool{"-m": true})
	if err != nil {
		return err
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if f.has("--embed") {
		emb, err := embed.ProviderFromEnv()
		if err != nil {
			return err
		}
		msg := f.get("-m", "index rebuild --embed")
		snapAddr, vecAddr, err := r.RebuildEmbeddings(ctx, emb, msg)
		if err != nil {
			return err
		}
		fmt.Printf("vec %s\nsnapshot %s\n", vecAddr, snapAddr)
		return nil
	}
	msg := f.get("-m", "index rebuild")
	snapAddr, rootAddr, err := r.RebuildIndex(ctx, msg)
	if err != nil {
		return err
	}
	fmt.Printf("index %s\nsnapshot %s\n", rootAddr, snapAddr)
	return nil
}
