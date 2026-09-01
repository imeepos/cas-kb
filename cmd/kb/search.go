package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
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
		type row struct {
			Path    string   `json:"path"`
			Slug    string   `json:"slug"`
			Addr    string   `json:"addr"`
			Title   string   `json:"title"`
			Tags    []string `json:"tags"`
			Summary string   `json:"summary"`
			Score   float64  `json:"score"`
		}
		rows := make([]row, 0, len(hits))
		for _, h := range hits {
			tags := h.Tags
			if tags == nil {
				tags = []string{}
			}
			rows = append(rows, row{Path: h.Path, Slug: h.Slug, Addr: string(h.Addr), Title: h.Title, Tags: tags, Summary: firstSummary(h.Body), Score: h.Score})
		}
		return printJSON(rows)
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
