// Package index 提供 M4 检索:确定性分词、内容寻址的倒排索引对象与 BM25 查询。
// 索引全部落为 CAS 对象,由快照引用,可复现、可审计、可结构共享(DESIGN §7)。
package index

import (
	"sort"
	"unicode"
)

// Term 是一个词元及其在文本中的出现次数(词频)。
type Term struct {
	Text  string
	Count int
}

// IsCJK 报告 rune 是否属于 CJK 表意文字/假名/谚文,走 2-gram 切分。
// 导出供展示层(片段高亮)复用同一套字符分类,保证与索引分词同口径。
func IsCJK(r rune) bool {
	switch {
	case r >= 0x3400 && r <= 0x4DBF, // CJK 扩展 A
		r >= 0x4E00 && r <= 0x9FFF, // CJK 基本区
		r >= 0xF900 && r <= 0xFAFF, // CJK 兼容表意
		r >= 0x3040 && r <= 0x309F, // 平假名
		r >= 0x30A0 && r <= 0x30FF, // 片假名
		r >= 0xAC00 && r <= 0xD7AF: // 谚文音节
		return true
	}
	return false
}

// IsWordChar 报告 rune 是否属于 ASCII 词元字符(字母/数字)。
// 导出供展示层(片段高亮)复用同一套字符分类,保证与索引分词同口径。
func IsWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// Tokenize 把文本切分为词元并统计词频,输出按词元字典序排列(确定性)。
// 规则:
//   - 全部 rune 先做 Unicode 小写归一;
//   - 连续 ASCII 字母/数字为一个词元(如 sha256、v2);
//   - CJK(汉字/假名/谚文)连续段按 2-gram 切分,单字成元;
//   - 其余字符(空白/标点/符号/非 ASCII 词字符)一律作分隔符。
func Tokenize(text string) []Term {
	lower := []rune(text)
	for i, r := range lower {
		lower[i] = unicode.ToLower(r)
	}
	freq := map[string]int{}
	var word []rune // ASCII 词元累积
	var cjk []rune  // CJK 连续段累积
	flushWord := func() {
		if len(word) > 0 {
			freq[string(word)]++
			word = word[:0]
		}
	}
	flushCJK := func() {
		switch len(cjk) {
		case 0:
		case 1:
			freq[string(cjk)]++
		default:
			for i := 0; i+1 < len(cjk); i++ {
				freq[string(cjk[i:i+2])]++
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range lower {
		switch {
		case IsWordChar(r):
			flushCJK()
			word = append(word, r)
		case IsCJK(r):
			flushWord()
			cjk = append(cjk, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	out := make([]Term, 0, len(freq))
	for t, c := range freq {
		out = append(out, Term{Text: t, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}
