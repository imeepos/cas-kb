package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/imeepos/cas-kb/internal/selfupdate"
)

// cmdUpdate 在线检查 GitHub 最新 Release;--yes 时下载、校验并替换本二进制。
func cmdUpdate(ctx context.Context, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--repo": true})
	if err != nil {
		return err
	}
	yes := f.has("--yes")
	repoName := f.get("--repo", os.Getenv("KB_UPDATE_REPO"))
	if repoName == "" {
		repoName = selfupdate.DefaultRepo
	}
	opts := selfupdate.Options{Repo: repoName, Token: os.Getenv("GITHUB_TOKEN")}
	fmt.Printf("当前版本: %s\n", version)
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := selfupdate.Latest(checkCtx, opts)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	published := ""
	if !rel.PublishedAt.IsZero() {
		published = fmt.Sprintf("(%s 发布)", rel.PublishedAt.Format("2006-01-02"))
	}
	fmt.Printf("最新版本: %s%s\n", rel.TagName, published)
	if version == "dev" {
		fmt.Println("当前为开发构建(dev),无法与 Release 比较版本")
	} else {
		switch selfupdate.CompareVersions(rel.TagName, version) {
		case -1, 0:
			fmt.Println("已是最新,无需更新")
			return nil
		}
		fmt.Printf("发现新版本: %s\n", rel.HTMLURL)
	}
	asset, err := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if !yes {
		fmt.Printf("平台产物: %s\n", asset.Name)
		fmt.Println("运行 kb update --yes 在线升级(下载后校验 sha256 并替换本二进制)")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: 定位本二进制失败: %w", err)
	}
	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("update: 解析本二进制路径失败: %w", err)
	}
	instCtx, cancel2 := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel2()
	fmt.Printf("下载并安装 %s → %s …\n", asset.Name, target)
	if err := selfupdate.Apply(instCtx, rel, target, opts); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fmt.Printf("已更新到 %s;下次运行 kb 即为新版本(kb version 可确认)\n", rel.TagName)
	return nil
}
