package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/view"
)

// TestSearchSnippetCLI T35/M4.2 验收:--snippet 文本模式命中行下追加缩进片段、
// --json 可选字段 snippet(缺省不带,向后兼容)、排序不变红线(带/不带
// --snippet 的结果序列逐行一致)、确定性(重复调用逐字节一致)、
// 标题命中(正文无词元)取开头窗口无标记。
func TestSearchSnippetCLI(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	body := strings.Repeat("开头铺垫文字。", 10) + "窗口中心命中" + strings.Repeat("。尾部补充", 10)
	must(cmdNote(ctx, []string{"set", "go/snippet/demo", "--title", "片段演示", "--body", body, "-m", "a"}))
	must(cmdNote(ctx, []string{"set", "misc/titleonly", "--title", "仅标题命中窗口", "--body", "正文完全无关的内容", "-m", "b"}))

	// 基线:不带 --snippet 的文本输出(命中行序列)
	plain, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"窗口"}) })
	must(err)
	// 文本 + --snippet:命中词元以【】包裹,缩进一行追加在命中行下
	with, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"窗口", "--snippet"}) })
	must(err)
	if !strings.Contains(with, "\n    ") {
		t.Fatalf("片段行应有缩进: %q", with)
	}
	lines := strings.Split(strings.TrimSuffix(with, "\n"), "\n")
	hitLines := make([]string, 0, len(lines))
	snippetSeen, titleOnlySnippet := false, ""
	for i, ln := range lines {
		if strings.HasPrefix(ln, "    ") {
			snippetSeen = true
			continue
		}
		if strings.Contains(ln, "go/snippet/demo") {
			if !strings.Contains(lines[i+1], "【窗口】") {
				t.Fatalf("正文命中应标记词元: %q", lines[i+1])
			}
		}
		if strings.Contains(ln, "misc/titleonly") {
			titleOnlySnippet = lines[i+1]
		}
		hitLines = append(hitLines, ln)
	}
	if !snippetSeen {
		t.Fatalf("应存在缩进片段行: %q", with)
	}
	// 红线:排序/命中集合零变化——过滤片段行后与基线逐行一致(路径+分数)
	plainLines := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	if strings.Join(hitLines, "\n") != strings.Join(plainLines, "\n") {
		t.Fatalf("--snippet 不得改变结果序列:\n带 %q\n不带 %q", hitLines, plainLines)
	}
	// 标题命中(body 无词元)钉死行为:开头窗口、无标记
	if titleOnlySnippet == "" || strings.Contains(titleOnlySnippet, "【") {
		t.Fatalf("仅标题命中应为开头窗口无标记: %q", titleOnlySnippet)
	}
	// --json + --snippet:snippet 字段存在且含标记
	jout, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"窗口", "--json", "--snippet"}) })
	must(err)
	if !strings.Contains(jout, "\"snippet\": \"") {
		t.Fatalf("--json --snippet 应含 snippet 字段: %q", jout)
	}
	var rows []view.SearchRow
	if err := json.Unmarshal([]byte(jout), &rows); err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, row := range rows {
		if row.Path == "go/snippet/demo" && strings.Contains(row.Snippet, "【窗口】") {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("JSON snippet 应含标记: %+v", rows)
	}
	// 向后兼容:--json 缺省不带 snippet 字段(旧消费者零破坏)
	jplain, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"窗口", "--json"}) })
	must(err)
	if strings.Contains(jplain, "\"snippet\"") {
		t.Fatalf("--json 缺省不得出现 snippet 字段: %q", jplain)
	}
	// 确定性:重复调用逐字节一致
	again, err := captureStdout(t, func() error { return cmdSearch(ctx, []string{"窗口", "--snippet"}) })
	must(err)
	if again != with {
		t.Fatal("同快照同查询 --snippet 输出应逐字节一致")
	}
}
