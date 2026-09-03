package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/view"
)

// embedFromEnv 按环境构造 hybrid 检索用的 Embedder;包级变量便于测试替换。
// KB_EMBED_MODEL 未设置时返回可行动报错(含配置方法与 rebuild --embed 指引),
// 绝不静默降级为纯词法。
var embedFromEnv = func() (embed.Embedder, error) {
	return embed.FromEnvWithNext(embed.NextHybridSearch)
}

// cmdSearch 处理 kb search:全文检索(BM25,结果确定性可复现)。
// 用法:kb search <query...> [--at 快照] [-n N] [--json] [--snippet] [--hybrid]
// --snippet 为纯展示层增量(M4.2):命中行下追加缩进片段(--json 时输出
// 可选字段 snippet),评分/排序/命中集合零变化(DESIGN §7.1)。
// --hybrid 为混合检索(M6-B,DESIGN §7.3):BM25 与向量余弦两路各取前 50 名
// 做 RRF 融合(k=60 固定常数),score 输出融合分;--json 时行内附带可选字段
// mode:"hybrid"(缺省不带,向后兼容)。前置:快照带 vec(rebuild --embed)
// 且进程配置 KB_EMBED_URL/KB_EMBED_MODEL;任一不满足即响亮报错,不降级。
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
	var hits []repo.SearchHit
	if f.has("--hybrid") {
		emb, err := embedFromEnv()
		if err != nil {
			return err
		}
		hits, err = r.SearchHybrid(ctx, query, f.get("--at", ""), emb)
		if err != nil {
			return err
		}
	} else {
		hits, err = r.Search(ctx, query, f.get("--at", ""))
		if err != nil {
			return err
		}
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
		// 行契约复用 internal/view,与 /api/v1/search 同构(TestServeCLIParity 钉死);
		// snippet 字段仅在 --snippet 时存在、mode 字段仅在 --hybrid 时存在
		//(均 omitempty,旧消费者零破坏)
		var rows []view.SearchRow
		if f.has("--snippet") {
			rows = view.SearchRowsWithSnippet(hits, query)
		} else {
			rows = view.SearchRows(hits)
		}
		if f.has("--hybrid") {
			rows = view.WithMode(rows, "hybrid")
		}
		return printJSON(rows)
	}
	for _, h := range hits {
		fmt.Printf("%.4f  %s  %s\n", h.Score, h.Path, h.Title)
		if f.has("--snippet") {
			if snip := view.Snippet(h.Body, query); snip != "" {
				fmt.Printf("    %s\n", snip)
			}
		}
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
