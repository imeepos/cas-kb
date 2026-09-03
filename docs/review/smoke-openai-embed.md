# M6 真模型冒烟演练报告:OpenAI 兼容嵌入端点

- **日期**:2026-09-02(美国太平洋时间)
- **角色**:验证者会话——只写本报告,不改任何产品代码
- **基线**:9ea6426(v0.7.0+M6-C),分支 `drill/openai-smoke`,独立 worktree
- **被测二进制**:`go build -o /tmp/drill-t59/kb ./cmd/kb`(工作树内构建;`kb dev`,darwin/arm64,go1.26.2)
- **临时库**:`/tmp/drill-t59/smoke.db`(SQLite,演练后不清理磁盘以备复核,不在工作树内)

## 1. 环境

| 项 | 值 |
|---|---|
| 嵌入端点 | OpenAI 兼容网关,基址 `https://***`(打码;可用路径为 `/v1/*` 前缀) |
| 鉴权 | Bearer(OPENAI_API_KEY,经 `/Users/imeepos/ext512/ymm-001/.env` 以 `set -a; . …; set +a` 注入;本报告与提交信息零密钥) |
| OPENAI_MODEL | `deepseek-v4-flash`(聊天模型,仅作备选,未用于嵌入) |
| 选中嵌入模型 | `Qwen/Qwen3-Embedding-4B`(选型规则:id 含 embed 优先) |
| 向量维度 | 2560 |
| 探测纪律 | 所有 curl 带 `--max-time` ≤30;连续两次超时即记 BLOCKED 换下一探测项 |

## 2. 腿 1:端点探测

| # | 探测 | HTTP | 耗时 | 结论 |
|---|---|---|---|---|
| 1 | GET `{BASE}/models` | 404 | 1.15s | `/models` 不可用,按计划试 `/v1/models` |
| 2 | GET `{BASE}/v1/models` | 200 | 0.67s | 23 个模型;id 含 embed:`Qwen/Qwen3-Embedding-4B`、`text-embedding-3-large` |
| 3 | POST `{BASE}/embeddings`(无 /v1,Qwen 模型) | 404 | 0.55s | 无 /v1 前缀的 embeddings 不存在 |
| 4 | POST `{BASE}/v1/embeddings` model=text-embedding-3-large | 404 | 3.48s | error 体:"The requested path format is incorrect"——清单里有但实际不可用 |
| 5 | POST `{BASE}/v1/embeddings` model=Qwen/Qwen3-Embedding-4B,input=`["冒烟"]` | **200** | 3.02s | 返回 `data[0].embedding`,**维度 2560**,usage.total_tokens=3 |

**选型结论**:`Qwen/Qwen3-Embedding-4B`(优先 id 含 embed;`text-embedding-3-large` 实测 404 排除;OPENAI_MODEL 为聊天模型不适用)。

**与产品契约的吻合**:`internal/embed/openai.go` 的 openai 提供者端点拼接为 `{OPENAI_BASE_URL}/v1/embeddings`(Bearer),与本次实测唯一可用的嵌入路径完全一致。

**过程注记(演练基础设施,非端点问题)**:探测早期 3 次调用在验证者工具通道层发生 300s 级挂起,且事后无任何落盘产物;定位为命令文本含非 ASCII 字符(中文)时工具层异常,与网关无关——改用纯 ASCII 命令 + `\uXXXX` 转义请求体后全部复测通过,上述表中状态均为复测实测值。BLOCKED 纪律未因端点本身触发。

## 3. 腿 2:真模型语义冒烟

**数据设计**(5 条中文笔记,标题+正文均**不含**「并发」「安全」二词,查询词只出现在查询里):

| 路径 | 主题 |
|---|---|
| `go/concurrency/basics`(Go 协程与通道) | body 只说 goroutine 与 channel 的协作、mutex 保护共享状态、避免数据竞争 |
| `life/cooking/pork` `life/travel/kyoto` `life/music/guitar` `life/fitness/run` | 做菜/旅游/音乐/健身四条无关主题 |

**步骤与实测**:

1. `kb init`(临时库)→ `kb bulk import notes.jsonl` → 5 条,snapshot `sha256:268b840c…`,exit 0。
   (过程注记:JSONL 的 `tags` 字段须为字符串数组;逗号串会被响亮拒绝——自造输入错误,非产品缺陷。)
2. 导出 `KB_EMBED_PROVIDER=openai`、`KB_EMBED_MODEL=Qwen/Qwen3-Embedding-4B` 后 `kb index rebuild --embed`:
   exit 0,**耗时 3.66s(5 条真模型嵌入)**,vec `sha256:0859f563…`,snapshot `sha256:3c634afa…`。
3. `kb search "并发安全" --hybrid -n 5 --json`:**`go/concurrency/basics` 命中且排第 1**(见 §5 表)。
4. `kb search "并发安全" -n 5 --json`(无 --hybrid):**(no results),exit 0——词法零命中**。
5. **关键断言成立**:BM25 零命中的同义笔记被 hybrid 召回且列第 1 ⇒ M6 语义检索对真模型有效。
6. `kb doctor`:**6 ok / 0 warn / 0 fail,exit 0**;嵌入配置(KB_EMBED_PROVIDER=openai + KB_EMBED_MODEL)在 config 检查中零告警。

**分数语义注记**:hybrid 输出分数为 RRF 融合分 `score=Σ 1/(60+rank)`(`internal/repo/hybrid.go`,RRFK=60、每路 top-50)。本查询 BM25 腿零命中,故排序即向量余弦腿排名——实测第 1~5 名分数 1/61…1/65 与该公式精确吻合;分数不携带余弦相似度幅度(设计行为,见 §7-E3)。

## 4. 腿 3:一致性

- 同一查询 `--hybrid` 连跑 3 次:**结果序列完全一致,RRF 分数抖动 0.00e+00**。
- 观测面说明:CLI 暴露的是融合分(排名派生),原始余弦值不外露;服务端查询嵌入的波动若不翻转排名则不可见,本次 3 连跑未出现任何排名级抖动 ⇒ 记「稳定」。
- 确定性契约边界(代码注释明示):同快照 + 同 model_id → 融合纯函数可复现;换模型必须 `rebuild --embed`(本演练未跨模型,边界未越)。
- `kb fsck`:检查 87 个对象,完整无问题,exit 0。

## 5. hybrid vs BM25 对比表(查询:「并发安全」)

| 排名 | 模式 BM25(默认) | 模式 hybrid(--hybrid) | hybrid 分数(RRF) |
|---|---|---|---|
| 1 | —(零命中) | `go/concurrency/basics` Go 协程与通道 | 0.016393 |
| 2 | — | `life/cooking/pork` 红烧肉做法 | 0.016129 |
| 3 | — | `life/music/guitar` 吉他练习 | 0.015873 |
| 4 | — | `life/fitness/run` 跑步计划 | 0.015625 |
| 5 | — | `life/travel/kyoto` 京都三日行 | 0.015385 |

BM25:命中 0 条;hybrid:命中 5 条(2×top-50 并集内全量返回),目标笔记第 1。

## 6. 腿 4:回归护栏

`./scripts/verify.sh` 两次执行:exit **0**——gofmt/build/vet 全过,10 个包单测全 `ok`,SQLite e2e `E2E_GREEN`(DRILL/KB_TEST_DSN 未设置按设计跳过)。演练全程未触碰工作树内文件,`git status` 在提交前仅含本报告。

## 7. 缺陷清单

**产品缺陷:无。**

环境/设计观察(不计缺陷,不阻塞):

- **E1(网关)**:`/models`(无 /v1 前缀)404,网关仅服务 `/v1/*`;产品 openai 提供者按 `{BASE}/v1/embeddings` 拼接,实测正确命中,无需改动。
- **E2(网关)**:`/v1/models` 清单中的 `text-embedding-3-large` 实际 POST 404——模型清单与可用性不符;嵌入模型选型应以实测单条嵌入为准(本演练即按此选中 Qwen)。
- **E3(设计行为)**:hybrid 输出分数为 RRF 融合分,不含余弦相似度幅度;下游若需相似度阈值需另行观测面。记录备查,非缺陷。

## 8. 结论

M6 语义检索(A:rebuild --embed / B:search --hybrid / C:openai 提供者)对真实 OpenAI 兼容嵌入端点(`Qwen/Qwen3-Embedding-4B`,2560 维)全链路可用:同义不同词召回有效、BM25 对照零命中形成干净反证、重复查询稳定、doctor/fsck/verify.sh 全绿。

VERDICT: PASS
