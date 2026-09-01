package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

func cmdGC(ctx context.Context, args []string) error {
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := r.GC(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("GC: 标记 %d,清扫 %d\n", res.Marked, res.Swept)
	return nil
}

// gcProtectOn 报告 GC 保护是否开启(KB_GC_PROTECT,默认 on)。
func gcProtectOn() bool {
	switch strings.ToLower(os.Getenv("KB_GC_PROTECT")) {
	case "off", "0", "false":
		return false
	}
	return true
}

// applyGCProtection 按 KB_GC_PROTECT 为仓库配置配置 GC 前的分支表导出。
func applyGCProtection(cfg *repo.Config) {
	if !gcProtectOn() {
		return
	}
	cfg.GCProtect = true
	cfg.GCExportBranches = exportBranchesFile
}

// backupRow 是备份文件的单行 JSON 契约:小写稳定键,不随内部类型变化。
type backupRow struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
}

// exportBranchesFile 把分支表写成当前目录下的 JSON 备份文件。
// 备份文件不自动清理,由运维按保留策略归档或删除。
func exportBranchesFile(ctx context.Context, branches []store.BranchRef) error {
	rows := make([]backupRow, 0, len(branches))
	for _, b := range branches {
		rows = append(rows, backupRow{Name: string(b.Name), Addr: string(b.Addr)})
	}
	payload, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("branches-backup-%d.json", time.Now().UnixNano())
	if err := os.WriteFile(name, payload, 0o644); err != nil {
		return err
	}
	fmt.Printf("GC 保护: 分支表已备份到 %s\n", name)
	return nil
}
