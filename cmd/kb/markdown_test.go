package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertTreesEqual 断言两个目录树的全部文件逐字节一致(相对路径集合与内容)。
func assertTreesEqual(t *testing.T, a, b string) {
	t.Helper()
	collect := func(root string) map[string][]byte {
		t.Helper()
		files := map[string][]byte{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = data
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	fa, fb := collect(a), collect(b)
	if len(fa) != len(fb) {
		t.Fatalf("目录树文件数不一致: %s=%d %s=%d(%v / %v)", a, len(fa), b, len(fb), keysOf(fa), keysOf(fb))
	}
	for rel, da := range fa {
		db, ok := fb[rel]
		if !ok {
			t.Fatalf("%s 缺少文件 %s", b, rel)
		}
		if string(da) != string(db) {
			t.Fatalf("文件 %s 逐字节不一致:\n%q\n%q", rel, da, db)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestMarkdownCLIExportImportRoundtrip:CLI 级 roundtrip 契约——
// export → 改库 → import → diff 零变更 → 再次 export 与首次逐字节一致。
func TestMarkdownCLIExportImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Chdir(t.TempDir())
	setNote(t, "a", "A")
	if err := cmdNote(ctx, []string{"set", "go/concurrency/channel", "--title", "通道", "--body", "chan 语义", "--tags", "go,并发", "-m", "add-chan"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"set", "plain", "--title", "无标签", "--body", "无标签正文", "-m", "add-plain"}); err != nil {
		t.Fatal(err)
	}
	before := headShort(t) // 导出前的快照短标识

	if err := cmdExport(ctx, []string{"md", "md1"}); err != nil {
		t.Fatal(err)
	}
	// 改库:改一条、删一条,再导入还原
	if err := cmdNote(ctx, []string{"set", "a", "--title", "A2", "--body", "changed", "-m", "tweak"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdNote(ctx, []string{"rm", "go/concurrency/channel", "-m", "rm-chan"}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdImport(ctx, []string{"md", "md1", "-m", "restore"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "import md 2 条") {
		t.Fatalf("还原导入应写回 2 条: %q", out)
	}
	out, err = captureStdout(t, func() error { return cmdDiff(ctx, []string{before, "main"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(no changes)") {
		t.Fatalf("重导后 diff 应零变更(地址不变): %q", out)
	}

	// 再次 export 与首次逐字节一致;--at 历史快照导出同样一致
	if err := cmdExport(ctx, []string{"md", "md2"}); err != nil {
		t.Fatal(err)
	}
	assertTreesEqual(t, "md1", "md2")
	if err := cmdExport(ctx, []string{"md", "md3", "--at", before}); err != nil {
		t.Fatal(err)
	}
	assertTreesEqual(t, "md1", "md3")

	// 对已一致的内容再导入:零变更、无新快照
	out, err = captureStdout(t, func() error { return cmdImport(ctx, []string{"md", "md2"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "无新快照") {
		t.Fatalf("内容未变时导入应零变更: %q", out)
	}
}

// TestMarkdownCLIExportForce:目标文件已存在时整批拒绝并提示 --force,--force 后覆盖。
func TestMarkdownCLIExportForce(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Chdir(t.TempDir())
	setNote(t, "a", "A")
	setNote(t, "b", "B")
	if err := cmdExport(ctx, []string{"md", "out"}); err != nil {
		t.Fatal(err)
	}
	// 预置一个无关的已存在文件:整批拒绝应包含全部冲突文件
	if err := os.WriteFile(filepath.Join("out", "b.md"), []byte("---\ntitle: 占位\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdExport(ctx, []string{"md", "out"})
	if err == nil {
		t.Fatal("目标已存在应拒绝")
	}
	if !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "b.md") {
		t.Fatalf("拒绝信息应含冲突文件与 --force 提示: %v", err)
	}
	if err := cmdExport(ctx, []string{"md", "out", "--force"}); err != nil {
		t.Fatalf("--force 应覆盖: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("out", "b.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "title: B") {
		t.Fatalf("--force 后应以库内容覆盖: %q", data)
	}
}

// TestMarkdownCLIImportProblems:缺 title、非 .md 文件、中间段是条目等
// 问题文件响亮列出并整批拒绝(不写入任何条目)。
func TestMarkdownCLIImportProblems(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	t.Chdir(t.TempDir())
	setNote(t, "a", "A")
	for _, d := range []string{"bad/nested", "bad/a"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"bad/ok.md":       "---\ntitle: OK\n---\n正文\n",
		"bad/no-title.md": "---\ntags: x\n---\n正文\n",
		"bad/extra.txt":   "不是 markdown",
		"bad/a.md":        "---\ntitle: A\n---\n条目挡路\n",
		"bad/a/sub.md":    "---\ntitle: Sub\n---\n子条目\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(rel, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := cmdImport(ctx, []string{"md", "bad"})
	if err == nil {
		t.Fatal("含问题文件应整批拒绝")
	}
	for _, want := range []string{"no-title.md", "extra.txt", "a.md", "a/sub.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("拒绝信息应列出问题文件 %s: %v", want, err)
		}
	}
	// 整批拒绝:合法的 ok.md 也不得写入
	out, err := captureStdout(t, func() error { return cmdNote(ctx, []string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "OK") || strings.Contains(out, "bad/") {
		t.Fatalf("整批拒绝后不应写入任何条目: %q", out)
	}
}

// TestMarkdownEntryPathValidation:相对路径 → 条目路径的合法性校验。
func TestMarkdownEntryPathValidation(t *testing.T) {
	ok := []string{"a.md", "a/b.md", "go/concurrency/channel.md"}
	for _, rel := range ok {
		if _, err := mdEntryPath(rel); err != nil {
			t.Fatalf("%s 应合法: %v", rel, err)
		}
	}
	bad := []string{".md", "a//b.md", "./a.md", "../a.md", "a/../b.md", "a/./b.md", "a/.md", "notes.txt", "a/b.TXT"}
	for _, rel := range bad {
		if _, err := mdEntryPath(rel); err == nil {
			t.Fatalf("%s 应报非法路径", rel)
		}
	}
}
