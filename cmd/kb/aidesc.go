package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// maxDescLen 是项目/分支描述的长度纪律上限(DESIGN §4.6,约定 ≤512 字符)。
const maxDescLen = 512

// summaryRunes 是 note ls --json 派生摘要的最大字符数(展示层派生,不改对象)。
const summaryRunes = 120

// printJSON 以缩进形式输出机器可读 JSON(非 ASCII 原样保留)。
func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// checkDesc 校验描述长度纪律。
func checkDesc(desc string) error {
	if n := len([]rune(desc)); n > maxDescLen {
		return fmt.Errorf("描述超长(%d/%d 字符)", n, maxDescLen)
	}
	return nil
}

// descOrEmpty 返回空描述的展示占位。
func descOrEmpty(desc string) string {
	if strings.TrimSpace(desc) == "" {
		return "(未设置)"
	}
	return desc
}

// firstSummary 从正文派生首个非空行摘要,超长截断,供 AI 粗筛;不改对象与地址。
func firstSummary(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rs := []rune(line)
		if len(rs) > summaryRunes {
			return string(rs[:summaryRunes]) + "…"
		}
		return line
	}
	return ""
}
