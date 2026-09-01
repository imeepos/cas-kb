package index

import (
	"reflect"
	"testing"
)

func terms(t *testing.T, text string) []Term {
	t.Helper()
	return Tokenize(text)
}

func TestTokenizeASCII(t *testing.T) {
	got := terms(t, "Hello, World! hello")
	want := []Term{{Text: "hello", Count: 2}, {Text: "world", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTokenizeWordCharsKeepDigits(t *testing.T) {
	got := terms(t, "sha256 与 v2")
	// 单个汉字单字成元
	want := []Term{{Text: "sha256", Count: 1}, {Text: "v2", Count: 1}, {Text: "与", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTokenizeCJKBigram(t *testing.T) {
	got := terms(t, "内容寻址")
	want := []Term{{Text: "内容", Count: 1}, {Text: "容寻", Count: 1}, {Text: "寻址", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 重复子串词频累加
	got = terms(t, "内容内容")
	want = []Term{{Text: "内容", Count: 2}, {Text: "容内", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 单字成元
	got = terms(t, "中")
	want = []Term{{Text: "中", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTokenizeMixed(t *testing.T) {
	got := terms(t, "Go语言chan 是 CSP 原语!")
	want := []Term{
		{Text: "chan", Count: 1},
		{Text: "csp", Count: 1},
		{Text: "go", Count: 1},
		{Text: "原语", Count: 1},
		{Text: "是", Count: 1},
		{Text: "语言", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTokenizeEmptyAndSeparators(t *testing.T) {
	if got := terms(t, ""); len(got) != 0 {
		t.Fatalf("空串应为空: %v", got)
	}
	if got := terms(t, " 。，！？---"); len(got) != 0 {
		t.Fatalf("纯分隔符应为空: %v", got)
	}
}

func TestTokenizeDeterministic(t *testing.T) {
	a := Tokenize("并发 Channel 与 Merkle 树内容寻址")
	b := Tokenize("并发 Channel 与 Merkle 树内容寻址")
	if !reflect.DeepEqual(a, b) {
		t.Fatal("同一输入两次分词应逐字节一致")
	}
	for i := 1; i < len(a); i++ {
		if a[i-1].Text >= a[i].Text {
			t.Fatalf("输出应按字典序: %v", a)
		}
	}
}
