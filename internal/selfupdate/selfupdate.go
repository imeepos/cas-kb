// Package selfupdate 实现 kb 的在线自更新:查询 GitHub 最新 Release、
// 语义化版本比较、按平台选取产物、sha256 校验后原子替换当前二进制。
// 产物命名与发布流水线(.github/workflows/release.yml)保持一致:
// kb-<版本>-<os>-<arch>.tar.gz(Windows 为 .zip)+ sha256sums.txt。
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo 是默认检查的 GitHub 仓库(owner/name)。
const DefaultRepo = "imeepos/cas-kb"

// DefaultAPIBase 是 GitHub API 根地址;测试可经 Options.APIBase 覆盖。
const DefaultAPIBase = "https://api.github.com"

// Options 控制更新行为;零值可用(仓库取 DefaultRepo,平台取运行时值)。
type Options struct {
	Repo    string       // owner/name,空则 DefaultRepo
	Client  *http.Client // HTTP 客户端,nil 则按用途给默认超时
	APIBase string       // API 根地址,空则 DefaultAPIBase(测试注入)
	Token   string       // 可选 GitHub API 令牌,缓解匿名速率限制
	GOOS    string       // 目标平台,空则 runtime.GOOS(测试注入)
	GOARCH  string       // 目标架构,空则 runtime.GOARCH(测试注入)
}

// Release 是 GitHub Release 的子集字段。
type Release struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset 是 Release 附件。
type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Latest 查询仓库的最新正式 Release(/releases/latest 不含 draft 与 prerelease)。
func Latest(ctx context.Context, o Options) (*Release, error) {
	repo := o.Repo
	if repo == "" {
		repo = DefaultRepo
	}
	base := o.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kb-selfupdate")
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: 查询 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var rel Release
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return nil, fmt.Errorf("selfupdate: 解析 Release 响应失败: %w", err)
		}
		if rel.TagName == "" {
			return nil, fmt.Errorf("selfupdate: Release 响应缺少 tag_name")
		}
		return &rel, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("selfupdate: 仓库 %s 暂无 Release(或仓库名有误),可到 https://github.com/%s/releases 查看", repo, repo)
	case http.StatusForbidden:
		return nil, fmt.Errorf("selfupdate: GitHub API 限流或拒绝(403),可设置 GITHUB_TOKEN 后重试")
	default:
		return nil, fmt.Errorf("selfupdate: GitHub API 返回状态 %d", resp.StatusCode)
	}
}

// CompareVersions 比较两个版本号(可带 v 前缀):a<b 得 -1,相等得 0,a>b 得 1。
// 逐段比较:数字前缀按数值;同数值时带后缀(预发布)小于无后缀;缺少的段按 0 补齐
// (因此 v0.1 与 v0.1.0 相等)。
func CompareVersions(a, b string) int {
	as := splitVer(a)
	bs := splitVer(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		if c := cmpSeg(segAt(as, i), segAt(bs, i)); c != 0 {
			return c
		}
	}
	return 0
}

func splitVer(v string) []string {
	return strings.Split(strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v"), ".")
}

func segAt(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "0"
}

// cmpSeg 比较单个版本段:数字前缀定大小,同数值时后缀空者大(正式版 > 预发布)。
func cmpSeg(a, b string) int {
	na, sa := splitNum(a)
	nb, sb := splitNum(b)
	if na != nb {
		if na < nb {
			return -1
		}
		return 1
	}
	if sa == sb {
		return 0
	}
	if sa == "" {
		return 1
	}
	if sb == "" {
		return -1
	}
	return strings.Compare(sa, sb)
}

// splitNum 拆出段首的十进制数字前缀(无数字视为 0)与其余后缀。
func splitNum(s string) (int, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s
	}
	return n, s[i:]
}

// AssetFor 选取平台产物 kb-<版本>-<goos>-<goarch>.tar.gz(Windows 为 .zip)。
func (r *Release) AssetFor(goos, goarch string) (*Asset, error) {
	ver := strings.TrimPrefix(r.TagName, "v")
	want := map[string]bool{
		fmt.Sprintf("kb-%s-%s-%s.tar.gz", ver, goos, goarch): true,
		fmt.Sprintf("kb-%s-%s-%s.zip", ver, goos, goarch):    true,
	}
	for i := range r.Assets {
		if want[r.Assets[i].Name] {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("selfupdate: Release %s 没有平台 %s/%s 的产物,请到 %s 手动下载",
		r.TagName, goos, goarch, r.pageURL())
}

// checksumAsset 取 sha256sums.txt 附件。
func (r *Release) checksumAsset() (*Asset, error) {
	for i := range r.Assets {
		if r.Assets[i].Name == "sha256sums.txt" {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("selfupdate: Release %s 缺少 sha256sums.txt,拒绝安装未校验产物;请到 %s 手动下载", r.TagName, r.pageURL())
}

func (r *Release) pageURL() string {
	if r.HTMLURL != "" {
		return r.HTMLURL
	}
	return "https://github.com/" + DefaultRepo + "/releases"
}

// Apply 下载并校验 rel 的平台产物,把其中的 kb 二进制替换到 target:
// 取 sha256sums.txt → 下载归档(边下边算哈希)→ 校验 → 解出二进制 → 原子替换;
// 任一步失败都保留原二进制。临时文件写在本二进制同目录(需要目录写权限)。
func Apply(ctx context.Context, rel *Release, target string, o Options) error {
	if target == "" {
		return fmt.Errorf("selfupdate: 目标路径为空")
	}
	goos, goarch := o.GOOS, o.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	asset, err := rel.AssetFor(goos, goarch)
	if err != nil {
		return err
	}
	sums, err := rel.checksumAsset()
	if err != nil {
		return err
	}
	sumsData, err := fetch(ctx, client, sums.BrowserDownloadURL, 1<<20)
	if err != nil {
		return err
	}
	want, ok := lookupChecksum(sumsData, asset.Name)
	if !ok {
		return fmt.Errorf("selfupdate: sha256sums.txt 中没有 %s 的校验值", asset.Name)
	}
	dir := filepath.Dir(target)
	archive, err := os.CreateTemp(dir, ".kb-update-archive-*")
	if err != nil {
		return fmt.Errorf("selfupdate: 建临时文件失败: %w", err)
	}
	archiveName := archive.Name()
	defer os.Remove(archiveName) // 归档始终是临时物,返回前删除
	h := sha256.New()
	if err := downloadTo(ctx, client, asset.BrowserDownloadURL, archive, h, 1<<30); err != nil {
		archive.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("selfupdate: 落盘归档失败: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("selfupdate: sha256 校验失败(期望 %s,实际 %s),已丢弃下载", want, got)
	}
	bin, err := extractBinary(archiveName, dir, goos)
	if err != nil {
		return err
	}
	defer os.Remove(bin) // 成功路径已 rename 走,此处为空操作
	return swap(bin, target)
}

// fetch 下载小文件(校验和清单)到内存,limit 限制最大字节数。
func fetch(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := doGet(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: 下载 %s 失败: %w", url, err)
	}
	return data, nil
}

// downloadTo 流式下载大文件到 w,同时把字节喂给哈希 h(可为 nil),limit 限制大小。
func downloadTo(ctx context.Context, client *http.Client, url string, w io.Writer, h io.Writer, limit int64) error {
	resp, err := doGet(ctx, client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.ContentLength > limit {
		return fmt.Errorf("selfupdate: 下载 %s 过大(%d 字节),放弃", url, resp.ContentLength)
	}
	src := io.LimitReader(resp.Body, limit)
	if h != nil {
		src = io.TeeReader(src, h)
	}
	if _, err := io.Copy(w, src); err != nil {
		return fmt.Errorf("selfupdate: 下载 %s 失败: %w", url, err)
	}
	return nil
}

func doGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: 构造下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "kb-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: 下载 %s 失败: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("selfupdate: 下载 %s 返回状态 %d", url, resp.StatusCode)
	}
	return resp, nil
}

// extractBinary 从归档解出 kb 二进制写入 dir 下的临时文件(0755),返回其路径。
// 归档格式按目标平台定:Windows 为 zip,其余为 tar.gz。
func extractBinary(archive, dir, goos string) (string, error) {
	want := "kb"
	if goos == "windows" {
		want = "kb.exe"
		return extractFromZip(archive, dir, want)
	}
	return extractFromTar(archive, dir, want)
}

func extractFromTar(archive, dir, want string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", fmt.Errorf("selfupdate: 打开归档失败: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("selfupdate: 归档不是有效 gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("selfupdate: 归档中找不到 %s", want)
		}
		if err != nil {
			return "", fmt.Errorf("selfupdate: 读取归档失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != want {
			continue
		}
		return writeBinary(tr, dir)
	}
}

func extractFromZip(archive, dir, want string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", fmt.Errorf("selfupdate: 归档不是有效 zip: %w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || filepath.Base(zf.Name) != want {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", fmt.Errorf("selfupdate: 读取 %s 失败: %w", zf.Name, err)
		}
		bin, err := writeBinary(rc, dir)
		rc.Close()
		return bin, err
	}
	return "", fmt.Errorf("selfupdate: 归档中找不到 %s", want)
}

func writeBinary(r io.Reader, dir string) (string, error) {
	bin, err := os.CreateTemp(dir, ".kb-update-bin-*")
	if err != nil {
		return "", fmt.Errorf("selfupdate: 建临时二进制失败: %w", err)
	}
	name := bin.Name()
	if _, err := io.Copy(bin, io.LimitReader(r, 1<<31)); err != nil {
		bin.Close()
		os.Remove(name)
		return "", fmt.Errorf("selfupdate: 写入二进制失败: %w", err)
	}
	if err := bin.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("selfupdate: 落盘二进制失败: %w", err)
	}
	if err := os.Chmod(name, 0o755); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("selfupdate: 设置可执行位失败: %w", err)
	}
	return name, nil
}

// swap 把 newBin 原子替换到 target:target→.old,newBin→target,失败回滚;
// 旧二进制删除尽力而为(Windows 上运行中的旧文件可能暂时删不掉)。
func swap(newBin, target string) error {
	old := target + ".old"
	_ = os.Remove(old)
	hadOld := true
	if err := os.Rename(target, old); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("selfupdate: 无法移开旧二进制: %w", err)
		}
		hadOld = false
	}
	if err := os.Rename(newBin, target); err != nil {
		if hadOld {
			_ = os.Rename(old, target)
		}
		return fmt.Errorf("selfupdate: 替换二进制失败: %w", err)
	}
	_ = os.Remove(old)
	return nil
}

// lookupChecksum 在 sha256sums 内容里找 name 的十六进制校验值
// (兼容 "哈希  文件名" 与 "哈希 *文件名" 两种格式)。
func lookupChecksum(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if strings.TrimPrefix(f[1], "*") == name {
			return f[0], true
		}
	}
	return "", false
}
