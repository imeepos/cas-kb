package view

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSnippetMixedCJKAndEnglish 中英混合查询:两类词元均被标记,
// 且保持正文原文字节(大写 CCCC 不被改写为小写)。
func TestSnippetMixedCJKAndEnglish(t *testing.T) {
	got := Snippet([]byte("aaaa,bbbb,CCCC-dddd;eeee"), "cccc")
	want := "aaaa,bbbb,【CCCC】-dddd;eeee"
	if got != want {
		t.Fatalf("片段不符:\n得到 %s\n期望 %s", got, want)
	}
	got = Snippet([]byte("go 并发编程:goroutine 与 channel 组合并发。"), "go 并发")
	if !strings.Contains(got, "【go】 【并发】") {
		t.Fatalf("多词元未按预期标记: %s", got)
	}
	if strings.Count(got, "【并发】") != 2 {
		t.Fatalf("两处「并发」都应标记: %s", got)
	}
	if strings.Contains(got, "【goroutine】") {
		t.Fatalf("goroutine 是整词,不应按 go 前缀命中: %s", got)
	}
}

// TestSnippetEnglishWordBoundary 英文按词边界:查询 chan 只标记独立的
// chan,channel / channels 不命中(与索引分词整词语义一致)。
func TestSnippetEnglishWordBoundary(t *testing.T) {
	got := Snippet([]byte("channel 与 chan 与 channels。"), "chan")
	want := "channel 与 【chan】 与 channels。"
	if got != want {
		t.Fatalf("词边界标记不符: 得到 %s,期望 %s", got, want)
	}
}

// TestSnippetCJKGramMergesToWordSource CJK 2-gram 匹配扩展回完整词源:
// 查询「知识库」(词元 知识/识库)合并标记为【知识库】,而非【知识】【识库】。
func TestSnippetCJKGramMergesToWordSource(t *testing.T) {
	got := Snippet([]byte("本库实现知识库系统,支持全文检索。"), "知识库")
	want := "本库实现【知识库】系统,支持全文检索。"
	if got != want {
		t.Fatalf("词源扩展不符: 得到 %s,期望 %s", got, want)
	}
	if strings.Contains(got, "【知识】【识库】") {
		t.Fatalf("不应按 2-gram 硬切标记: %s", got)
	}
}

// TestSnippetNoBodyHit 无任何词元命中 body(如仅标题命中)时的钉死行为:
// 片段取 body 开头同等窗口(80 rune)、无标记(DESIGN §7.1 二选一之选定)。
func TestSnippetNoBodyHit(t *testing.T) {
	body := []byte(strings.Repeat("无命中内容补足长度。", 10)) // 100 rune
	got := Snippet(body, "absent")
	want := strings.Repeat("无命中内容补足长度。", 8) // 恰 80 rune
	if got != want {
		t.Fatalf("无命中应取开头 80 rune 无标记,得到 %q", got)
	}
	if strings.ContainsRune(got, '【') {
		t.Fatalf("无命中不应产生标记: %s", got)
	}
	// 边缘:查询无词元 / body 为空
	if got := Snippet(body, ""); got != want {
		t.Fatalf("空查询应与无命中同规,得到 %q", got)
	}
	if got := Snippet(nil, "词"); got != "" {
		t.Fatalf("空正文应返回空串,得到 %q", got)
	}
}

// TestSnippetWindowEdgeSnapsToSeparator 窗口边缘吸附:截断的左右边缘
// 落在标点/空白之后,不劈开词语;目标窗口约 80 rune。
func TestSnippetWindowEdgeSnapsToSeparator(t *testing.T) {
	body := []byte(strings.Repeat("前导文字。", 20) + "关键词" + strings.Repeat("。后续文字", 20))
	got := Snippet(body, "关键词")
	if !strings.Contains(got, "【关键词】") {
		t.Fatalf("命中词元应被标记: %s", got)
	}
	if n := utf8.RuneCountInString(got) - 2; n > 80 { // 减去【】两枚标记 rune
		t.Fatalf("窗口应约 80 rune,得到 %d: %s", n, got)
	}
	if !strings.HasSuffix(got, "。") {
		t.Fatalf("右缘应吸附在标点之后: %s", got)
	}
	if strings.HasPrefix(got, "字。") {
		t.Fatalf("左缘应吸附在标点之后而非词语中间: %s", got)
	}
	// 确定性:重复调用逐字节一致
	for i := 0; i < 5; i++ {
		if again := Snippet(body, "关键词"); again != got {
			t.Fatalf("片段应逐字节确定,第 %d 次不符", i)
		}
	}
}

// TestSnippetRuneBoundary 窗口按 rune 切,不劈开多字节字符:
// 纯 CJK 长文与 emoji(非 CJK 非词字符)长文均输出合法 UTF-8。
func TestSnippetRuneBoundary(t *testing.T) {
	body := []byte(strings.Repeat("汉", 100) + "字")
	got := Snippet(body, "汉字")
	if !utf8.ValidString(got) {
		t.Fatalf("片段应为合法 UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "【汉字】") {
		t.Fatalf("命中应完整可见且被标记: %s", got)
	}
	if n := utf8.RuneCountInString(got); n != 44 { // 42 rune 窗口 + 【】
		t.Fatalf("rune 数应为 44,得到 %d", n)
	}
	body = []byte(strings.Repeat("\U0001F680", 60) + "目标词" + strings.Repeat("\U0001F680", 60))
	got = Snippet(body, "目标词")
	if !utf8.ValidString(got) {
		t.Fatalf("四字节 emoji 不得被劈开: %q", got)
	}
	if !strings.Contains(got, "【目标词】") {
		t.Fatalf("命中词元应被标记: %s", got)
	}
}

// TestSnippetMatchesTokenizerSemantics 标记语义与索引分词同口径:
// sha256 只整词命中(sha256sum 不算),孤字「检」只命中段长为 1 的孤字
// (「检索检索」里的检不成元),与 BM25 实际匹配的词元完全一致。
func TestSnippetMatchesTokenizerSemantics(t *testing.T) {
	got := Snippet([]byte("sha256sum 与 检索检索 检"), "sha256 检")
	want := "sha256sum 与 检索检索 【检】"
	if got != want {
		t.Fatalf("分词同口径标记不符: 得到 %s,期望 %s", got, want)
	}
	// 2-gram 重复出现:全部出现合并为一段连续标记
	got = Snippet([]byte("检索检索"), "检索")
	if got != "【检索检索】" {
		t.Fatalf("相邻 2-gram 应合并为一段标记: %s", got)
	}
}
