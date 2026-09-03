package repo

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	evaleval "github.com/imeepos/cas-kb/tests/eval"
)

// evalDims 是评测假嵌入器的固定维度(16 轴主题空间)。
const evalDims = 16

// evalAxes 是「主题词 → 轴」手工固定表(M6-B 评测的核心夹具):同义词/
// 上下位词/中英混写映射到同一轴——语义近旁 = 共享主题轴,与字面 token 无关。
// 表是评测口径的一部分,改动即改评测,须同步核对 recall 断言。
var evalAxes = map[string]int{
	// axis 0:部署/发布(同义:部署 deploy rollout ↔ 上线 发布 回滚)
	"部署": 0, "deploy": 0, "rollout": 0, "上线": 0, "发布": 0, "回滚": 0,
	// axis 1:容器/镜像
	"docker": 1, "镜像": 1, "容器": 1, "image": 1, "k8s": 1, "kubernetes": 1,
	// axis 2:数据库(上下位:数据库 → postgres/mysql/sql)
	"postgres": 2, "postgresql": 2, "mysql": 2, "sql": 2, "数据库": 2,
	// axis 3:缓存
	"redis": 3, "缓存": 3, "cache": 3, "命中率": 3, "memcached": 3,
	// axis 4:接口
	"接口": 4, "api": 4, "rest": 4, "json": 4, "http": 4, "端点": 4, "graphql": 4, "网关": 4,
	// axis 5:安全/认证(同义:认证 token 密码 ↔ 鉴权 权限 登录)
	"认证": 5, "token": 5, "密码": 5, "鉴权": 5, "权限": 5, "授权": 5, "安全": 5, "登录": 5, "jwt": 5,
	// axis 6:性能(同义:延迟 p99 ↔ 响应 慢 优化)
	"性能": 6, "延迟": 6, "latency": 6, "p99": 6, "响应": 6, "慢": 6, "优化": 6,
	// axis 7:检索(上下位:搜索 → bm25 倒排/向量 语义)
	"检索": 7, "搜索": 7, "bm25": 7, "倒排": 7, "向量": 7, "embedding": 7, "语义": 7, "索引": 7, "召回": 7, "余弦": 7,
	// axis 8:并发(同义:goroutine/channel ↔ 协程)
	"并发": 8, "goroutine": 8, "channel": 8, "锁": 8, "mutex": 8, "竞态": 8, "协程": 8,
	// axis 9:监控(上下位:观测 → metrics 告警 指标)
	"监控": 9, "metrics": 9, "告警": 9, "观测": 9, "prometheus": 9, "指标": 9,
	// axis 10:备份(同义:backup 恢复 ↔ 找回 容灾)
	"备份": 10, "backup": 10, "恢复": 10, "容灾": 10, "找回": 10,
	// axis 11:文档/笔记(同义:笔记 文档 知识库 ↔ 备忘录)
	"笔记": 11, "文档": 11, "markdown": 11, "知识库": 11, "知识": 11, "备忘录": 11,
	// axis 14:日志/追踪
	"日志": 14, "log": 14, "trace": 14, "追踪": 14, "排查": 14,
	// axis 15:错误码
	"错误码": 15,
}

// evalEmbedder 是评测假 Embedder:文本经 evalAxes 命中的主题词各给其轴 +1,
// 再做 L2 归一(无命中词 = 零向量)。同文本恒同向量(纯函数,确定性);
// 语义近旁 = 共享主题轴,与字面 token 是否重叠无关——正是语义腿区别于
// BM25 的性质。查询与条目共用同一嵌入器,与真实链路同构。
type evalEmbedder struct{}

func (evalEmbedder) Model() string { return "eval-fake-embed" }
func (evalEmbedder) Dim() int      { return evalDims }

func (evalEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, evalDims)
		for kw, axis := range evalAxes {
			if strings.Contains(t, kw) {
				v[axis]++
			}
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			s := float32(math.Sqrt(norm))
			for j := range v {
				v[j] /= s
			}
		}
		out[i] = v
	}
	return out, nil
}

// seedEvalCorpus 把固定评测语料写入临时库并重建向量索引,返回仓库。
func seedEvalCorpus(t *testing.T) *Repo {
	t.Helper()
	ctx := context.Background()
	r, _, _ := newRepo(t, "hybrid_eval")
	for _, e := range evaleval.Entries {
		if _, _, err := r.SetNote(ctx, e.Path, NoteInput{Title: e.Title, Body: e.Body}, "eval seed "+e.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.RebuildEmbeddings(ctx, evalEmbedder{}, "eval embed rebuild"); err != nil {
		t.Fatal(err)
	}
	return r
}

// recallAt5 计算前 5 名对相关集的召回率;相关集为空约定返回 1(全命中)。
func recallAt5(hits []SearchHit, relevant []string) float64 {
	rel := map[string]bool{}
	for _, p := range relevant {
		rel[p] = true
	}
	if len(rel) == 0 {
		return 1
	}
	got := 0
	for i, h := range hits {
		if i >= 5 {
			break
		}
		if rel[h.Path] {
			got++
		}
	}
	return float64(got) / float64(len(rel))
}

// TestHybridEval M6-B 验收:固定语料(tests/eval,23 条中文知识条目)上,
// 假 Embedder(主题轴固定表,确定性)钉死三件事:
//  1. 语义类查询(12 条,同义不同词/上下位/中英混写)hybrid recall@5
//     逐条严格优于纯 BM25(证明语义增强有效,不是感觉);
//  2. 代码/ID 类查询(3 条)两种模式都命中前 5(证明没有伤害词法精度);
//  3. 融合确定性:同快照 + 同向量数据,重复调用结果与顺序完全一致。
func TestHybridEval(t *testing.T) {
	ctx := context.Background()
	r := seedEvalCorpus(t)

	// 评测集规模红线:≥20 条知识条目 + ≥10 条语义查询(验收口径固化)
	sem, lex := 0, 0
	for _, q := range evaleval.Queries {
		switch q.Kind {
		case "semantic":
			sem++
		case "lexical":
			lex++
		}
	}
	if len(evaleval.Entries) < 20 {
		t.Fatalf("评测语料应 ≥20 条,得到 %d", len(evaleval.Entries))
	}
	if sem < 10 {
		t.Fatalf("语义类查询应 ≥10 条,得到 %d", sem)
	}
	if lex < 1 {
		t.Fatal("代码/ID 类查询应 ≥1 条")
	}

	var semBetter int
	for _, q := range evaleval.Queries {
		bm, err := r.Search(ctx, q.Text, "")
		if err != nil {
			t.Fatalf("BM25 检索 %q: %v", q.Text, err)
		}
		hy, err := r.SearchHybrid(ctx, q.Text, "", evalEmbedder{})
		if err != nil {
			t.Fatalf("hybrid 检索 %q: %v", q.Text, err)
		}
		rb, rh := recallAt5(bm, q.Relevant), recallAt5(hy, q.Relevant)
		t.Logf("%-16s q=%-24s bm25@5=%.3f hybrid@5=%.3f", q.Kind, q.Text, rb, rh)
		switch q.Kind {
		case "semantic":
			if rh <= rb {
				t.Errorf("语义查询 %q:hybrid recall@5=%.3f 应严格优于 BM25=%.3f", q.Text, rh, rb)
			}
			if rh != 1 {
				t.Errorf("语义查询 %q:hybrid recall@5 应为 1,得到 %.3f(hits=%v)", q.Text, rh, pathsOf(hy))
			}
			semBetter++
		case "lexical":
			if rb < 1 || rh < 1 {
				t.Errorf("代码/ID 查询 %q:两模式都应命中(top5),bm25=%.3f hybrid=%.3f", q.Text, rb, rh)
			}
			if len(hy) == 0 || hy[0].Path != q.Relevant[0] {
				t.Errorf("代码/ID 查询 %q:hybrid 首名应为 %s,得到 %+v", q.Text, q.Relevant[0], pathsOf(hy))
			}
		}
	}
	if semBetter < 10 {
		t.Fatalf("语义类查询应 ≥10 条严格改善,得到 %d", semBetter)
	}

	// 确定性:同快照 + 同向量数据 → 结果与顺序逐字段一致(可复现性边界 =
	// 同快照 + 同 model_id,DESIGN §7.3)
	for _, q := range evaleval.Queries {
		a, err := r.SearchHybrid(ctx, q.Text, "", evalEmbedder{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.SearchHybrid(ctx, q.Text, "", evalEmbedder{})
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != len(b) {
			t.Fatalf("确定性破坏:长度 %d vs %d", len(a), len(b))
		}
		for i := range a {
			if a[i].Path != b[i].Path || a[i].Score != b[i].Score || a[i].Addr != b[i].Addr {
				t.Fatalf("确定性破坏(查询 %q 第 %d 位):%+v vs %+v", q.Text, i, a[i], b[i])
			}
		}
	}
}

// pathsOf 抽取命中路径序列(失败信息用)。
func pathsOf(hits []SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

// addrOfFirst 返回首名地址(供其他测试引用)。
func addrOfFirst(hits []SearchHit) hash.Address {
	if len(hits) == 0 {
		return ""
	}
	return hits[0].Addr
}
