package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"v0.1.1", "v0.1.0", 1},
		{"v0.1.0", "v0.1.1", -1},
		{"v0.2.0", "v0.1.9", 1},
		{"v0.10.0", "v0.9.0", 1},
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v0.1", "v0.1.0", 0}, // 缺段按 0 补齐
		{"0.0.9", "v0.1.0", -1},
		{"v1.0", "1.0.1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d,期望 %d", c.a, c.b, got, c.want)
		}
	}
}

func platformAssets(tag string) []Asset {
	ver := strings.TrimPrefix(tag, "v")
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return []Asset{
		{Name: "kb-" + ver + "-linux-amd64.tar.gz"},
		{Name: "kb-" + ver + "-linux-arm64.tar.gz"},
		{Name: "kb-" + ver + "-darwin-amd64.tar.gz"},
		{Name: "kb-" + ver + "-darwin-arm64.tar.gz"},
		{Name: "kb-" + ver + "-windows-amd64.zip"},
		{Name: "kb-" + ver + "-" + runtime.GOOS + "-" + runtime.GOARCH + ext},
		{Name: "sha256sums.txt"},
	}
}

func TestAssetFor(t *testing.T) {
	rel := &Release{TagName: "v0.2.0", Assets: platformAssets("v0.2.0")}
	a, err := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("AssetFor 意外失败: %v", err)
	}
	ver := "0.2.0"
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	if want := "kb-" + ver + "-" + runtime.GOOS + "-" + runtime.GOARCH + wantExt; a.Name != want {
		t.Errorf("选中 %s,期望 %s", a.Name, want)
	}
	// 无匹配产物 → 报错并提示手动下载
	_, err = rel.AssetFor("plan9", "arm")
	if err == nil || !strings.Contains(err.Error(), "手动下载") {
		t.Errorf("缺失平台应报可读错误,得到: %v", err)
	}
}

func TestLatest(t *testing.T) {
	var gotAuth, gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/imeepos/cas-kb/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"tag_name":"v0.3.0","html_url":"http://x/rel","published_at":"2026-08-30T00:00:00Z","assets":[{"name":"kb-0.3.0-darwin-arm64.tar.gz"}]}`)
	})
	mux.HandleFunc("/repos/none/none/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	})
	mux.HandleFunc("/repos/rate/rate/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "API rate limit exceeded", http.StatusForbidden)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	rel, err := Latest(context.Background(), Options{APIBase: ts.URL, Token: "tok", Client: ts.Client()})
	if err != nil {
		t.Fatalf("Latest 失败: %v", err)
	}
	if rel.TagName != "v0.3.0" || rel.HTMLURL != "http://x/rel" {
		t.Errorf("解析结果不符: %+v", rel)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "kb-0.3.0-darwin-arm64.tar.gz" {
		t.Errorf("assets 解析不符: %+v", rel.Assets)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("未带上令牌头: %q", gotAuth)
	}
	if gotUA != "kb-selfupdate" {
		t.Errorf("缺少 User-Agent: %q", gotUA)
	}

	if _, err := Latest(context.Background(), Options{APIBase: ts.URL, Repo: "none/none", Client: ts.Client()}); err == nil || !strings.Contains(err.Error(), "暂无 Release") {
		t.Errorf("404 应提示暂无 Release,得到: %v", err)
	}
	if _, err := Latest(context.Background(), Options{APIBase: ts.URL, Repo: "rate/rate", Client: ts.Client()}); err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("403 应提示令牌,得到: %v", err)
	}
}

// buildArchive 在内存里构造与发布流水线同构的归档(kb-<版本>-<平台>/kb)。
func buildArchive(t *testing.T, tag, goos, goarch, bin string) ([]byte, string) {
	t.Helper()
	name := "kb"
	if goos == "windows" {
		name = "kb.exe"
	}
	top := tag + "-" + goos + "-" + goarch
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(top + "/" + name)
		if err != nil {
			t.Fatalf("zip 创建失败: %v", err)
		}
		if _, err := w.Write([]byte(bin)); err != nil {
			t.Fatalf("zip 写入失败: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("zip 收尾失败: %v", err)
		}
	} else {
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)
		if err := tw.WriteHeader(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatalf("tar 目录头失败: %v", err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(bin))}); err != nil {
			t.Fatalf("tar 文件头失败: %v", err)
		}
		if _, err := tw.Write([]byte(bin)); err != nil {
			t.Fatalf("tar 写入失败: %v", err)
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("tar 收尾失败: %v", err)
		}
		if err := gzw.Close(); err != nil {
			t.Fatalf("gzip 收尾失败: %v", err)
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestApply(t *testing.T) {
	for _, tc := range []struct {
		name, goos, goarch string
	}{
		{"本平台", runtime.GOOS, runtime.GOARCH},
		{"windows 平台走 zip", "windows", "amd64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newBin := "#!/bin/sh\necho new-kb\n"
			archive, shaHex := buildArchive(t, "v0.2.0", tc.goos, tc.goarch, newBin)
			ext := ".tar.gz"
			if tc.goos == "windows" {
				ext = ".zip"
			}
			assetName := "kb-0.2.0-" + tc.goos + "-" + tc.goarch + ext

			mux := http.NewServeMux()
			mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
				w.Write(archive)
			})
			mux.HandleFunc("/dl/sha256sums.txt", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, "%s  %s\n", shaHex, assetName)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			rel := &Release{TagName: "v0.2.0", HTMLURL: ts.URL, Assets: []Asset{
				{Name: assetName, BrowserDownloadURL: ts.URL + "/dl/" + assetName},
				{Name: "sha256sums.txt", BrowserDownloadURL: ts.URL + "/dl/sha256sums.txt"},
			}}
			dir := t.TempDir()
			target := filepath.Join(dir, "kb")
			if err := os.WriteFile(target, []byte("old-bin"), 0o755); err != nil {
				t.Fatalf("准备旧二进制失败: %v", err)
			}
			if err := Apply(context.Background(), rel, target, Options{GOOS: tc.goos, GOARCH: tc.goarch, Client: ts.Client()}); err != nil {
				t.Fatalf("Apply 失败: %v", err)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != newBin {
				t.Errorf("替换后内容不符: %q, err=%v", got, err)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatalf("stat 目标失败: %v", err)
			}
			if info.Mode().Perm()&0o100 == 0 {
				t.Errorf("替换后应保留可执行位: %v", info.Mode())
			}
			// 目录里不应残留临时物或 .old
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("目录应只剩目标文件,实际: %v", names)
			}
		})
	}
}

func TestApplyChecksumMismatch(t *testing.T) {
	newBin := "new-bin-bytes"
	archive, _ := buildArchive(t, "v0.2.0", runtime.GOOS, runtime.GOARCH, newBin)
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	assetName := "kb-0.2.0-" + runtime.GOOS + "-" + runtime.GOARCH + ext

	mux := http.NewServeMux()
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/dl/sha256sums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "deadbeef  "+assetName+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	rel := &Release{TagName: "v0.2.0", HTMLURL: ts.URL, Assets: []Asset{
		{Name: assetName, BrowserDownloadURL: ts.URL + "/dl/" + assetName},
		{Name: "sha256sums.txt", BrowserDownloadURL: ts.URL + "/dl/sha256sums.txt"},
	}}
	dir := t.TempDir()
	target := filepath.Join(dir, "kb")
	if err := os.WriteFile(target, []byte("old-bin"), 0o755); err != nil {
		t.Fatalf("准备旧二进制失败: %v", err)
	}
	err := Apply(context.Background(), rel, target, Options{Client: ts.Client()})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("校验失败应报 sha256 错误,得到: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old-bin" {
		t.Errorf("校验失败后原二进制必须原样保留,实际: %q", got)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("不应残留 .old: %v", err)
	}
}

func TestLookupChecksum(t *testing.T) {
	sums := []byte("abc123  kb-0.2.0-darwin-arm64.tar.gz\ndef456  *kb-0.2.0-windows-amd64.zip\n")
	if v, ok := lookupChecksum(sums, "kb-0.2.0-darwin-arm64.tar.gz"); !ok || v != "abc123" {
		t.Errorf("普通格式未命中: %q %v", v, ok)
	}
	if v, ok := lookupChecksum(sums, "kb-0.2.0-windows-amd64.zip"); !ok || v != "def456" {
		t.Errorf("星号格式未命中: %q %v", v, ok)
	}
	if _, ok := lookupChecksum(sums, "nope"); ok {
		t.Errorf("不存在条目不应命中")
	}
}
