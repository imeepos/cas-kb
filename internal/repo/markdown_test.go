package repo

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

// mdTestNotes 是 roundtrip 用的一组笔记:覆盖无标签、嵌套目录、
// 空正文、正文末行无换行等边界。
func mdTestNotes() []MdNote {
	return []MdNote{
		{Path: "hello", Title: "你好", Body: "你好,世界\n"},
		{Path: "empty", Title: "空正文", Body: ""},
		{Path: "go/concurrency/channel", Title: "通道", Tags: []string{"go", "并发"}, Body: "chan 语义\n\n第二段(末行无换行)"},
	}
}

// TestMarkdownEncodeDecodeRoundtrip:编解码往返逐字节一致(front-matter 严格格式)。
func TestMarkdownEncodeDecodeRoundtrip(t *testing.T) {
	for _, d := range mdTestNotes() {
		data := EncodeMdNote(d)
		got, err := DecodeMdNote(d.Path, data)
		if err != nil {
			t.Fatalf("解码 %s: %v", d.Path, err)
		}
		if got.Path != d.Path || got.Title != d.Title || got.Body != d.Body || !tagsEqual(got.Tags, d.Tags) {
			t.Fatalf("roundtrip 字段不一致:\ngot  %+v\nwant %+v", got, d)
		}
		// 再编码逐字节一致(export(import(X)) == X 的基础)
		if !bytes.Equal(EncodeMdNote(got), data) {
			t.Fatalf("roundtrip 字节不一致: %s\n%q\n%q", d.Path, data, EncodeMdNote(got))
		}
	}
	// 无标签时省略 tags 行
	data := EncodeMdNote(MdNote{Path: "x", Title: "T", Body: "b"})
	if strings.Contains(string(data), "tags:") {
		t.Fatalf("无标签应省略 tags 行: %q", data)
	}
	if want := "---\ntitle: T\n---\nb"; string(data) != want {
		t.Fatalf("编码不符: %q", data)
	}
}

// TestMarkdownDecodeErrors:front-matter 违规响亮报错(错误信息含文件路径)。
func TestMarkdownDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"缺少首行", "title: T\n---\nb", "首行"},
		{"缺少 title", "---\ntags: a\n---\nb", "title"},
		{"title 为空", "---\ntitle:\n---\nb", "title"},
		{"未闭合", "---\ntitle: T\nbody 没有闭合", "未闭合"},
		{"无法识别的行", "---\ntitle: T\ncreated: 2020-01-01\n---\nb", "无法识别"},
	}
	for _, c := range cases {
		if _, err := DecodeMdNote("f/x.md", []byte(c.data)); err == nil {
			t.Fatalf("%s 应报错", c.name)
		} else if !strings.Contains(err.Error(), "f/x.md") || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s 错误信息应含路径与原因: %v", c.name, err)
		}
	}
}

// TestMarkdownRepoRoundtrip 钉死两条 roundtrip 契约:
//  1. export(import(X)) 与 X 逐字节一致(全新库导入后导出);
//  2. import(export(库)) 写回后 diff 零变更、地址不变——
//     未改动的库重导不产生新快照;改动/删除后重导能逐字节还原。
func TestMarkdownRepoRoundtrip(t *testing.T) {
	ctx := context.Background()
	// 时间源逐次递增:若导入不复用旧对象的 CreatedAt,重写必然改变地址
	dsn := freshDB(t)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tick := int64(0)
	r := Open(s, Config{Branch: "mdrt", Now: func() int64 { tick++; return fixedTime + tick }})

	origAddr := map[string]string{}
	for _, d := range mdTestNotes() {
		if _, _, err := r.SetNote(ctx, d.Path, NoteInput{Title: d.Title, Body: d.Body, Tags: d.Tags}, "add "+d.Path); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range mdTestNotes() {
		ref, err := r.Note(ctx, d.Path)
		if err != nil {
			t.Fatal(err)
		}
		origAddr[d.Path] = string(ref.Addr)
	}
	head1, has, err := r.head(ctx)
	if err != nil || !has {
		t.Fatalf("读取头快照: %v %v", has, err)
	}

	// X = 导出文件的字节;契约 2(未改动库):import(export(库)) 零变更
	docs, err := r.ExportMarkdown(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != len(mdTestNotes()) {
		t.Fatalf("导出条数不符: %d", len(docs))
	}
	res, err := r.ImportMarkdown(ctx, docs, "reimport")
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 0 || res.Unchanged != len(docs) || res.Snapshot != "" {
		t.Fatalf("未改动库重导应零变更且无新快照: %+v", res)
	}
	if head2, _, _ := r.head(ctx); head2 != head1 {
		t.Fatal("零变更导入不得推进分支头")
	}

	// 契约 2(改动/删除后):重导逐字节还原,地址不变
	if _, _, err := r.SetNote(ctx, "hello", NoteInput{Title: "你好(改)", Body: "被改动的正文"}, "tweak hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveNote(ctx, "go/concurrency/channel", "rm channel"); err != nil {
		t.Fatal(err)
	}
	res, err = r.ImportMarkdown(ctx, docs, "restore")
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Unchanged != 1 {
		t.Fatalf("还原导入应写回 2 条(1 条未变): %+v", res)
	}
	changes, err := r.Diff(ctx, string(head1), "mdrt")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("重导后 diff 应零变更: %+v", changes)
	}
	for _, d := range mdTestNotes() {
		ref, err := r.Note(ctx, d.Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(ref.Addr) != origAddr[d.Path] {
			t.Fatalf("条目 %s 地址改变: %s -> %s", d.Path, origAddr[d.Path], ref.Addr)
		}
	}

	// 契约 1:export(import(X)) 与 X 逐字节一致(全新库)
	r2, _, _ := newRepo(t, "mdfresh")
	if _, err := r2.ImportMarkdown(ctx, docs, "md import"); err != nil {
		t.Fatal(err)
	}
	docs2, err := r2.ExportMarkdown(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, d := range docs {
		files[d.Path] = EncodeMdNote(d)
	}
	if len(docs2) != len(files) {
		t.Fatalf("再导出条数不符: %d != %d", len(docs2), len(files))
	}
	for _, d := range docs2 {
		want, ok := files[d.Path]
		if !ok {
			t.Fatalf("多余条目: %s", d.Path)
		}
		if !bytes.Equal(EncodeMdNote(d), want) {
			t.Fatalf("export(import(X)) 与 X 不一致: %s\n%q\n%q", d.Path, want, EncodeMdNote(d))
		}
	}
}

// TestMarkdownImportEmpty:空导入与导出到空分支的边界。
func TestMarkdownImportEmpty(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "mdempty")
	if _, err := r.ImportMarkdown(ctx, nil, "x"); err == nil {
		t.Fatal("空导入应报错")
	}
	docs, err := r.ExportMarkdown(ctx, "")
	if err != nil || len(docs) != 0 {
		t.Fatalf("空库导出应为 0 条: %d %v", len(docs), err)
	}
}
