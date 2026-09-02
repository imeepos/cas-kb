package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/view"
)

// cmdSearch 处理 kb search:全文检索(BM25,结果确定性可复现)。
// 用法:kb search <query...> [--at 快照] [-n N] [--json]
func cmdSearch(ctx context.Context, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--at": true, "-n": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("search: 缺少查询词")
	}
	query := strings.Join(f.pos, " ")
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	hits, err := r.Search(ctx, query, f.get("--at", ""))
	if err != nil {
		return err
	}
	if limit := f.get("-n", ""); limit != "" {
		hits, err = truncateHits(hits, limit)
		if err != nil {
			return err
		}
	}
	if len(hits) == 0 {
		fmt.Println("(no results)")
		return nil
	}
	if f.has("--json") {
		// 行契约复用 internal/view,与 /api/v1/search 同构(TestServeCLIParity 钉死)
		return printJSON(view.SearchRows(hits))
	}
	for _, h := range hits {
		fmt.Printf("%.4f  %s  %s\n", h.Score, h.Path, h.Title)
	}
	return nil
}

// truncateHits 按数量上限截断结果(-n,正整数)。
func truncateHits(hits []repo.SearchHit, limit string) ([]repo.SearchHit, error) {
	n := 0
	for _, ch := range limit {
		if ch < '0' || ch > '9' {
			return nil, fmt.Errorf("search: -n 需要正整数,得到 %q", limit)
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return nil, fmt.Errorf("search: -n 需要正整数,得到 %q", limit)
	}
	if n >= len(hits) {
		return hits, nil
	}
	return hits[:n], nil
}
