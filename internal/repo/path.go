package repo

import (
	"fmt"
	"strings"
)

// PathSep 是条目与目录路径的分隔符。
const PathSep = "/"

// ParsePath 把用户提供的路径解析为组件列表。
// 空串表示根目录(返回空组件表);其余按 "/" 切分,
// 每段不得为空、纯空白、"." 或 ".."。不改变段内其他字符。
func ParsePath(p string) ([]string, error) {
	if strings.TrimSpace(p) == "" {
		return []string{}, nil
	}
	parts := strings.Split(p, PathSep)
	for i, c := range parts {
		if strings.TrimSpace(c) == "" {
			return nil, fmt.Errorf("repo: 路径 %q 第 %d 段为空", p, i+1)
		}
		if c == "." || c == ".." {
			return nil, fmt.Errorf("repo: 路径 %q 含保留段 %q", p, c)
		}
	}
	return parts, nil
}

// SplitNotePath 把条目路径拆为目录组件与叶子 slug。
// 条目路径至少一个组件(仅根目录一条目:"a");"a/b/c" = 目录 a/b + 条目 c。
func SplitNotePath(p string) (dirs []string, slug string, err error) {
	parts, err := ParsePath(p)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("repo: 条目路径不能为空")
	}
	return parts[:len(parts)-1], parts[len(parts)-1], nil
}

// JoinPath 把组件表拼回路径字符串;空组件表返回空串(根目录)。
func JoinPath(parts []string) string { return strings.Join(parts, PathSep) }
