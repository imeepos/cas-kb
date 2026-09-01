package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

// cmdBackup 导出整库为 .ckb 备份文件(JSONL,含逐对象哈希)。
// 备份是全库语义,不受 -p/KB_PROJECT 影响。
func cmdBackup(ctx context.Context, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	out := fmt.Sprintf("caskb-v%d-backup-%s.ckb", store.DBSchemaVersion, time.Now().Format("20060102-150405"))
	if len(f.pos) >= 1 {
		out = f.pos[0]
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	fOut, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("backup: 创建文件失败: %w", err)
	}
	defer fOut.Close()
	stats, err := repo.DumpLibrary(ctx, s, fOut)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	fmt.Printf("备份完成: %s\n对象 %d · 项目 %d · 分支 %d\n", out, stats.Objects, stats.Projects, stats.Branches)
	return nil
}

// cmdRestore 从 .ckb 备份恢复整库;目标非空需 --force(先清空)。
func cmdRestore(ctx context.Context, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("restore: 缺少备份文件")
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	file, err := os.Open(f.pos[0])
	if err != nil {
		return fmt.Errorf("restore: 打开备份失败: %w", err)
	}
	defer file.Close()
	stats, err := repo.RestoreLibrary(ctx, s, file, f.has("--force"))
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	fmt.Printf("恢复完成: 对象 %d · 项目 %d · 分支 %d\n建议运行 kb fsck 复核\n", stats.Objects, stats.Projects, stats.Branches)
	return nil
}

// cmdWipe 清空整库并重置为全新初始化状态;破坏性操作,需 --force。
func cmdWipe(ctx context.Context, args []string) error {
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()

	// 预览将被清空的内容
	objects := 0
	errStop := fmt.Errorf("stop")
	_ = s.List(ctx, func(store.ObjectInfo) error { objects++; return errStop })
	branches, _ := s.BranchListAll(ctx)
	projects, _ := s.ProjectStats(ctx)
	if !f.has("--force") {
		fmt.Printf("将清空:对象 %d · 分支 %d · 项目 %d\n此操作不可撤销,确认请加 --force\n", objects, len(branches), len(projects))
		return nil
	}
	if err := s.Wipe(ctx); err != nil {
		return err
	}
	fmt.Printf("已清空并重置为全新库(schema v%d)\n", store.DBSchemaVersion)
	return nil
}
