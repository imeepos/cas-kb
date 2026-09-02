// snippet.go 实现 M4.2 检索片段高亮(DESIGN §7.1):纯展示层增量——
// 在检索结果序列确定后逐条附加「命中片段」,评分/排序/命中集合零变化。
// 算法确定性:同一 body 与 query,输出逐字节一致;不读时钟、不用随机。
package view

import (
	"sort"
	"strings"
	"unicode"

	"github.com/imeepos/cas-kb/internal/index"
)

// 片段窗口参数(DESIGN §7.1):目标 80 rune,以首个命中为中心、
// 前后各约 40 rune;发生截断的边缘向内最多回望 20 rune 吸附到分隔符。
const (
	snippetWindowRunes = 80
	snippetHalfWindow  = 40
	snippetSnapLook    = 20
)

// span 是正文中一个半开区间 [start, end)(rune 下标)。
type span struct{ start, end int }

// Snippet 从 body 原文截取命中片段,窗口内查询词元出现处以【】包裹:
//   - 查询词元经与索引同一套分词(index.Tokenize)得到;
//   - 以任一词元的首次出现为中心截取约 80 rune 窗口(rune 对齐,
//     不劈开多字节字符;截断边缘吸附标点/空白);
//   - ASCII 词元按整词相等(词边界,chan 不命中 channel);
//   - CJK 2-gram 在 CJK 连续段内取全部子串出现,孤字词元仅命中
//     段长为 1 的孤字(与索引「单字成元」同语义);命中区间合并,
//     2-gram 因此扩展回完整词源(查询「知识库」→【知识库】)。
//
// 无任何词元命中 body(如仅标题命中)时:取 body 开头同等窗口、无标记;
// body 为空返回空串。该二选一行为由 TestSnippetNoBodyHit 钉死(DESIGN §7.1)。
func Snippet(body []byte, query string) string {
	rs := []rune(string(body))
	if len(rs) == 0 {
		return ""
	}
	spans := matchSpans(rs, query)
	if len(spans) == 0 {
		// 无命中:开头窗口、无标记(钉死行为,见 DESIGN §7.1)
		n := len(rs)
		if n > snippetWindowRunes {
			n = snippetWindowRunes
		}
		return string(rs[:n])
	}
	start, end := windowOf(rs, spans[0])
	return markSpans(rs, start, end, spans)
}

// matchSpans 找出 query 全部词元在正文(小写归一后)的出现区间,
// 返回按起点升序、重叠/相接已合并的区间表;无词元或无命中返回 nil。
func matchSpans(rs []rune, query string) []span {
	terms := index.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	// 与 index.Tokenize 同款小写归一(Go 的 unicode.ToLower 是 1:1 rune
	// 映射,小写副本与原文的 rune 下标一一对应,区间可直接用于原文)。
	lower := make([]rune, len(rs))
	for i, r := range rs {
		lower[i] = unicode.ToLower(r)
	}
	var out []span
	add := func(s span) { out = append(out, s) }
	for i := 0; i < len(lower); {
		switch {
		case index.IsWordChar(lower[i]): // ASCII 词:整词相等才算命中(词边界)
			j := i + 1
			for j < len(lower) && index.IsWordChar(lower[j]) {
				j++
			}
			word := string(lower[i:j])
			for _, t := range terms {
				if t.Text == word {
					add(span{i, j})
					break
				}
			}
			i = j
		case index.IsCJK(lower[i]): // CJK 连续段:2-gram 子串出现 / 孤字整段相等
			j := i + 1
			for j < len(lower) && index.IsCJK(lower[j]) {
				j++
			}
			run := string(lower[i:j])
			for _, t := range terms {
				rt := []rune(t.Text)
				if len(rt) == 0 || !index.IsCJK(rt[0]) {
					continue // 只处理 CJK 词元(ASCII 词已在上一分支处理)
				}
				n := len(rt)
				if n == 1 { // 孤字词元:仅当整段就是单字(索引「单字成元」)
					if run == t.Text {
						add(span{i, j})
					}
					continue
				}
				for p := 0; p+n <= j-i; p++ { // 2-gram:段内全部子串出现
					if string(lower[i+p:i+p+n]) == t.Text {
						add(span{i + p, i + p + n})
					}
				}
			}
			i = j
		default:
			i++
		}
	}
	return mergeSpans(out)
}

// mergeSpans 按起点排序并合并重叠/相接的区间(确定性)。
func mergeSpans(sp []span) []span {
	if len(sp) == 0 {
		return nil
	}
	sort.Slice(sp, func(i, j int) bool {
		if sp[i].start != sp[j].start {
			return sp[i].start < sp[j].start
		}
		return sp[i].end < sp[j].end
	})
	out := []span{sp[0]}
	for _, s := range sp[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// isSeparator 报告 rune 是否为分隔符(标点/空白/符号),与索引分词的
// 「其余字符一律作分隔符」同口径:既非 ASCII 词元字符、也非 CJK。
func isSeparator(r rune) bool { return !index.IsWordChar(r) && !index.IsCJK(r) }

// windowOf 以首个命中 first 为中心截取窗口:起点 = 命中前约 40 rune,
// 终点 = 起点 + 约 80 rune;发生截断的边缘向内最多回望 20 rune 吸附到
// 最近的分隔符(截在分隔符之后),且首个命中必须完整可见。
func windowOf(rs []rune, first span) (start, end int) {
	start = first.start - snippetHalfWindow
	if start < 0 {
		start = 0
	}
	if start > 0 { // 左缘截断:向内吸附分隔符,不越过命中起点
		limit := start + snippetSnapLook
		if limit > first.start {
			limit = first.start
		}
		for k := start; k < limit; k++ {
			if isSeparator(rs[k]) {
				start = k + 1
				break
			}
		}
	}
	end = start + snippetWindowRunes
	if end < first.end {
		end = first.end // 首个命中完整可见
	}
	if end > len(rs) {
		end = len(rs)
	}
	if end < len(rs) { // 右缘截断:向内吸附分隔符,不切进命中
		lo := end - snippetSnapLook
		if lo < start {
			lo = start
		}
		for k := end - 1; k >= lo; k-- {
			if isSeparator(rs[k]) {
				end = k + 1
				break
			}
		}
		if end < first.end {
			end = first.end
		}
		if end > len(rs) {
			end = len(rs)
		}
	}
	return start, end
}

// markSpans 输出 rs[start:end],落在窗口内的命中区间以【】包裹
// (与窗口边缘相交的区间按窗口裁剪);spans 须已合并、按起点升序。
func markSpans(rs []rune, start, end int, spans []span) string {
	var b strings.Builder
	i := start
	for _, s := range spans {
		if s.end <= start || s.start >= end {
			continue
		}
		a, z := s.start, s.end
		if a < start {
			a = start
		}
		if z > end {
			z = end
		}
		b.WriteString(string(rs[i:a]))
		b.WriteString("【")
		b.WriteString(string(rs[a:z]))
		b.WriteString("】")
		i = z
	}
	b.WriteString(string(rs[i:end]))
	return b.String()
}
