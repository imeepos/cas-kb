# cas-kb 语义检索(向量)增强设计调研(T53)

> 任务:T53 纯文档调研(不含任何代码)· 分支 `research/vector-search`(独立 worktree,不合并不推送)· 交付物 = 本文件(唯一新增,不修改任何代码与既有文档)
> 检索日期:2026-09-02 ~ 09-03;全部证据链接为本次检索实测取得(curl / 官方文档抓取 / 线上 API / 本机实验),「检索不到」的项如实标注,不做编造(诚实清单见 §7)
> 负责人四条红线(既定,本文只做落地不做挑战):**(a)** 不内嵌模型运行时,嵌入走外挂服务(首选 Ollama 本机 HTTP);未配置嵌入服务 = 行为与 v0.7.0 完全一致;**(b)** 不引入向量数据库服务;向量按现有 64 桶分片存 CAS(拟新增对象 kind:vecshard),规模内平扫余弦;**(c)** 向量按 model_id 版本化,对象地址=哈希(向量字节+model_id);模型不匹配响亮报错要求 rebuild,绝不静默降级;「同快照可复现」收窄为「同快照+同模型」;**(d)** BM25 默认不动;混合检索做显式旗标
> 每问结构:**cas-kb 现状 → 社区做法(证据)→ 裁剪建议 → 落地改动清单草案**;§6 汇总为 M6 实施清单表(A/B 两批)供负责人直接立项

## 0. TL;DR

1. **融合公式(问 1):RRF,k=60 硬编码,零旋钮。**出处 = Cormack/Clarke/Büttcher(SIGIR 2009)原文:「k=60 was fixed during a pilot investigation and not altered during subsequent validation」——k=60 是试点期定死、验证期未动的常数,这正是它「无 tunable」的原因:以无参数换跨域稳健(BEIR 摘要同时提醒「BM25 是稳健基线」,倒逼融合必须显式而非默认)。Elastic 的 rank_constant 默认 60、Qdrant 默认 k=2(公式映射为论文的 61)——引擎各玩各的,论文 60 是唯一公共锚点。cas-kb v1 只做 RRF(60);加权分数融合(CC/DBSF/relativeScoreFusion)需要归一化与标注调参(Bruch TOIS 2023:RRF 对参数敏感、CC 更优但需样本),列为演进项。
2. **嵌入服务契约(问 2):只用 Ollama `/api/embed`(batch 原生),不碰 legacy `/api/embeddings`。**官方 api.md 实抓:`input` 接受字符串或数组(batch)、`truncate` 默认 true(设 false 超上下文即报错)、`keep_alive` 默认 5m、响应 `embeddings[][]`。模型钉住策略:model_id 配置化 + 首推自检维度 + 模型名进 vecroot;模型卡维度(本机 registry 与 HF config 实测):nomic-embed-text 768 维/8192 ctx(v1.5)、bge-m3 1024 维/8192 ctx(多语言,中文首选)、mxbai-embed-large 1024 维/512 ctx、all-minilm 384 维。本机未运行 Ollama(探测空),契约以官方文档为准——诚实见 §7。
3. **向量入 CAS(问 3):vecshard = JSON 定序头 + float32 小端二进制体,不用纯 JSON 存数值。**一手实验:同一 float32 的 LE 字节与 JSON 文本是两条确定性通路,但 JSON 形态依赖 Go stdlib 浮点格式化算法(官方先例:Go 1.8 明文声明 compress/flate 编码输出跨版本可变)且体积 2~3 倍;二进制定长 LE 只有一个自由度(端序),约定即永续。gzip 先例(indexshard −60%)**不可平移**:高熵向量本机实测 gzip 后仅剩 92%(省 8%),对照全零数据压 1000 倍——压缩对向量趋零,机制保留(0x01 前缀透明容器)、收益不指望。
4. **平扫边界(问 4):本机纯 Go 实测,10k×768 单查询 1.8ms、100k×1024 24ms——CPU 永远不是瓶颈,装载才是。**50ms 预算内纯计算可容 ~25 万×768;计入 CAS 读出+解码后安全线 = 数万条,十万条进入 50~100ms 区间,恰与 DESIGN §7「目标 ≤ 十万条」量级判断咬合。HNSW 不适合 CAS 冻结分片(hnswlib API 实证:add_items 增量改图/mark_deleted 墓碑/resize——图结构无「未动子结构地址不变」可言,结构共享归零);观测挂钩 = §7.2 指标 6,增行「混合检索延迟 P95(平扫段/嵌入段分开计)」,触发线:平扫段 P95 > 50ms 且条目数趋势增长。
5. **评测(问 5):20~50 条中文小评测集 + 三类查询 + 三条验收线,验收「真的有效」而非「好多少」。**社区惯例(recall@k / nDCG@10、先比三路再谈调参)照搬;cas-kb 草案:条目 30~50、查询 15~25、qrels 人工标注;查询分「词面命中(不倒退)/ 同义改写(语义增益)/ 概念联想」三类;验收线:①词面子集混合 recall@5 ≥ 纯 BM25(不倒退)②改写子集混合 recall@5 显著高于 BM25(语义真的有效,线值在基线固定后校准)③同快照+同模型两次运行结果序列逐字节一致。小样本=冒烟级判据,如实标注非学术结论。
6. **M6 实施清单(§6):A 批=对象模型与 rebuild(schema v6 + vecshard/vecroot 编解码 + Embedder 接口 + `kb vec rebuild`);B 批=检索集成与 API(`--hybrid` 旗标 + RRF 融合 + HTTP `hybrid=1` + doctor 检查项 + 评测集与观测挂钩)。**每批附验收标准草案;未配置嵌入服务时两条批次的全部行为与 v0.7.0 逐字节一致(红线 a 是验收的一部分,不是口号)。

---

## 1. 调研项 1:混合检索融合(RRF / 加权分数 / 何时默认)

### 1.1 cas-kb 现状

- 检索 = 纯 BM25(DESIGN §7):k1=1.2/b=0.75,字段加权标题 3/标签 2/正文 1,多词 OR 归并;**确定性排序:分数降序 → 路径升序 → 地址**。无任何语义通道。
- 快照携带 `index`(indexroot 地址,可选字段 omitempty)——这是「检索派生数据进快照」的既有形态,向量索引应镜像它而非另起炉灶。
- HTTP `/api/v1/search` 与 CLI `--json` 同一份契约(internal/view,parity 测试钉死);`snippet=1` 先例确立了「可选查询参数只认字面 1」的增量纪律。
- 原稿遗留:DESIGN §7「与原设计的差异」提到「语义向量检索同法处理(IVF 聚类分片),列为演进项」——红线 b 已把它改判为「固定 64 桶 + 平扫」,本文 §3/§4 给出改判依据,落地时 DESIGN 该句需随 M6 修订。

### 1.2 社区做法与证据

| 系统/文献 | 做法 | 证据(2026-09-02/03 实抓) |
|---|---|---|
| **RRF 原始论文**(Cormack, Clarke, Büttcher, SIGIR 2009) | `RRF_score(d) = Σ 1/(k + r(d))`;**「k=60 was fixed during a pilot investigation and not altered during subsequent validation」**——k 无 tunable 的出处即此句:试点定死、验证未动,以无参数换稳健;RRF 一致优于单系统与 Condorcet Fuse(TREC + LETOR 3 验证,无监督) | PDF 实抓并抽取正文:https://plg.uwaterloo.ca/~gvcormac/cormacksigir09-rrf.pdf |
| **Elasticsearch**(RRF retriever) | `rank_constant` **「Defaults to 60」**(≥1);`rank_window_size` **「Defaults to 10」**;支持每路 retriever 乘 `weight`(`score = Σ weight_i × rrf_score_i`)——RRF 是 retriever 组合之一,**不是默认检索形态** | 官方参考实抓:https://www.elastic.co/docs/reference/elasticsearch/rest-apis/retrievers/rrf-retriever |
| **Weaviate**(双融合算法) | `rankedFusion`(=RRF,博客原文「computed according to `1/(RANK + 60)`」)默认至 v1.24;`relativeScoreFusion`(min-max 归一 + alpha 加权 scaled sum)v1.24 起默认;alpha 控词法/向量权重(文档示例 0.5、0.25) | 官方博客源文实抓:https://github.com/weaviate/weaviate-io/blob/main/blog/2023-08-29-hybrid-search-fusion/index.mdx ;文档页实抓:https://docs.weaviate.io/weaviate/search/hybrid (「Relative Score Fusion is the default fusion method starting in v1.24」) |
| **Qdrant**(调参文章,2026-08-22) | **「Qdrant defaults to k=2. The original RRF paper uses 60, which maps to k=61 in Qdrant's formula」**——引擎默认值各异,论文 60 是公共锚点;方法论:**先确认 fusion 打赢两路 prefetch 再谈调参**,评分用 nDCG@10;RRF 只看排名,DBSF 用分布归一携带分差信息 | 官方博客实抓:https://qdrant.tech/articles/how-to-tune-hybrid-search/ |
| **Bruch et al.**(Pinecone,TOIS 2023) | 摘要原文(实抓):「**we find RRF to be sensitive to its parameters**; … **CC outperforms RRF** in in-domain and out-of-domain settings; … CC is sample efficient, requiring only a small set of training examples」——加权分数融合理论上限更高,**但需要标注样本调参** | https://arxiv.org/abs/2210.11934 (ar5iv 全文实抓) |
| **BEIR**(Thakur et al., 2021) | 摘要原文(实抓):「**BM25 is a robust baseline** and re-ranking and late-interaction-based models … achieve the best zero-shot performances … dense and sparse-retrieval models are computationally more efficient but often underperform」——词法基线不可轻视,融合收益必须按数据集实测,不存在「开了必赚」 | arXiv API 实抓:https://arxiv.org/abs/2104.08663 |

**横向归纳**:(a) RRF 是唯一「无参数、免训练、跨域稳健」的融合公式,论文的 k=60 被三巨头原样或映射采纳;(b) 加权分数融合族(relativeScoreFusion/CC/DBSF)都需要「归一化决策 + 可调权重」,收益依赖标注样本;(c) **没有任何主流引擎把混合检索做成无旗标默认**——Elastic 要显式用 rrf retriever,Weaviate/Qdrant 的 hybrid 是查询参数;「混合当默认」的社区叙事实际是「默认提供 hybrid 能力」,不是「默认打开」。这与红线 d(显式旗标)完全同向。

### 1.3 对 cas-kb 的裁剪建议

1. **v1 融合公式 = RRF,k 硬编码 60,不暴露配置**。理由:(a) 论文原文自证 k=60 是「不调的常数」,暴露成配置只会诱导无标注调参(Qdrant 文章的调参前提是「自有标注集」,cas-kb 没有);(b) 无参数 ⇒ 融合行为跨版本恒定,服务「同快照+同模型可复现」;(c) Bruch 的反证(RRF 参数敏感、CC 更优)恰恰说明可调融合是另一个工程(需要评测集与标注),v1 不做,列演进项。
2. **每路取 top-W 再融合**:BM25 路与向量路各取 top-W(W = max(50, 5×n),n 为用户 `-n`;Elastic 默认 10 偏小,其文档自述「higher value will improve result relevance at the cost of performance」,cas-kb 平扫+BM25 双路的边际成本可忽略,取大窗口换召回)。
3. **混合排序确定性沿用 M4 纪律**:RRF 分数降序 → 路径升序 → 地址;向量路浮点求和按固定遍历序(条目地址升序)保证同快照+同模型逐位一致。
4. **默认与旗标(红线 d 落地形态)**:`kb search --hybrid` 显式开启;无旗标 = 纯 BM25,与 v0.7.0 逐字节一致。HTTP 侧 `GET /api/v1/search?hybrid=1`(仅字面 1 生效,沿 `snippet=1` 先例)。**不采纳** Weaviate 式 alpha 权重(依赖 relativeScoreFusion 语义,属加权融合族,v1 不做)。
5. **失败语义即红线 c/d 的出口**:配了嵌入服务但快照无 vecroot → `--hybrid` 响亮报错指引 `kb vec rebuild`;vecroot.model_id ≠ 当前 KB_EMBED_MODEL → 响亮报错模型不匹配、要求 rebuild,**绝不静默回退纯 BM25**(静默降级会让「开了旗标」与「没开」不可区分,违背响亮失败纪律)。未配置嵌入服务时 `--hybrid` 报错指配置,`search` 不带旗标行为不变。

### 1.4 落地改动清单草案(供裁剪)

- [ ] internal/repo(或新 internal/search):RRF 融合函数(k=60 常量,含出处注释);两路 top-W 截断;确定性 tie-break
- [ ] `kb search --hybrid` 旗标;无 vecroot/模型不匹配/未配置嵌入三态响亮报错文案(各配可行动指引)
- [ ] `search --json` 契约增量:**v1 建议不加字段**——score 直接输出 RRF 融合分并在文档声明,契约面最小(保持 internal/view 一份实现纪律)
- [ ] DESIGN §7 增小节「混合检索(M6)」:公式、W、tie-break、失败语义;§7 老句「IVF 聚类分片演进项」改判说明
- [ ] CHANGELOG + ROADMAP M6 行
- **不做**:alpha/权重参数、reranker、DBSF/CC 调参、把 hybrid 升默认

---

## 2. 调研项 2:嵌入服务契约(Ollama /api/embed 与 Embedder 接口)

### 2.1 cas-kb 现状

- 无任何嵌入通道;配置面 8 个 KB_*(DESIGN §8.2),全部是本地语义(存储/分支/项目/令牌),无外部服务变量先例。
- 「未配置 = 行为与上一版本完全一致」有现成范式:写入 API 未配置令牌 = 纯只读(403,§8.6)。嵌入服务未配置 = 语义层整体不存在,是最自然的镜像。

### 2.2 社区做法与证据(全部一手实抓)

**Ollama 官方 api.md**(raw.githubusercontent.com/ollama/ollama main 分支,2026-09-02 实抓;本机 11434 探测无服务,故引官方文档,见 §7):

- `POST /api/embed`(现行端点):
  - 参数:`model`;`input`——**「text or list of text」**(batch 原生);`truncate`(「truncates the end of each input to fit within context length. **Returns error if false and context length is exceeded**. Defaults to true」);`keep_alive`(默认 5m);`dimensions`(声明输出维度)。
  - 响应:`{"model", "embeddings": [[...], [...]], "total_duration", "load_duration", "prompt_eval_count"}`——**embeddings 与 input 等长**,双精度 JSON 数组。
- `POST /api/embeddings`(legacy):官方原文「**this endpoint has been superseded by /api/embed**」;参数仅 `model`+`prompt`(单条),响应 `{"embedding": [...]}`。**无 batch,不采用**。
- 错误语义钩子:超上下文 + truncate=false → 报错(这是「超长条目响亮失败」的服务端支点);模型未拉取/加载失败 → HTTP 错误(api.md 未穷举错误码枚举,实现按非 2xx 归一处理)。

**模型钉住的 registry 实测**(registry.ollama.ai Docker-registry v2 API,curl 实抓 manifest 与 config blob):

| 模型 | registry 实测 | 维度/上下文(HF config.json 实测,经 hf-mirror) | cas-kb 定位 |
|---|---|---|---|
| nomic-embed-text | `model_type: 137M`,family nomic-bert,F16,模型层 274MB;**v1.5 tag params blob = `{"num_ctx":8192}`** | hidden_size **768**,max_position_embeddings 2048(HF 卡 8192 语境为 RoPE 扩展配置,以 Ollama num_ctx 为准) | 英文/混合默认候选 |
| bge-m3 | `model_type: 566.70M`,family bert,模型层 1.16GB,无 params blob | hidden_size **1024**,max_position_embeddings **8194**(可用 8192) | **中文首选**(官方卡:多语言 100+、dense/sparse/colbert 三功能、8192 tokens;cas-kb 只用 dense 通道) |
| mxbai-embed-large | `model_type: 334M`,params = `{"num_ctx":512}` | hidden_size **1024** | 英文短文本 |
| all-minilm(MiniLM-L6-v2) | (registry 未单测) | hidden_size **384**,max_position_embeddings 512 | 低配/快速 |

### 2.3 对 cas-kb 的裁剪建议(Embedder 接口草案)

1. **接口最小集(Go 形态,纯规格)**:

   ```go
   type Embedder interface {
       ModelID() string                                  // KB_EMBED_MODEL 原样,进 vecroot 供版本比对
       Embed(ctx, texts []string) ([][]float32, error)   // 单接口双用:1 条 = 查询路径,N 条 = 构建路径(batch ≤ 32)
   }
   ```

   `Dim()` 不设为方法:维度 = 首次 Embed 响应长度,由调用方缓存进 vecroot.dim 并在后续校验——**维度以服务实测为准,不信任配置声明**(这也是 api.md 提供 `dimensions` 参数但 cas-kb 不使用的原因:不接受服务端降维,维度变了就该 rebuild 而不是静默适配)。
2. **错误语义四态 + 超长,全部响亮,无静默**:

   | 错误 | 触发 | 出口 |
   |---|---|---|
   | ErrEmbedderUnconfigured | KB_EMBED_URL 未设 | 语义层不存在:search 纯 BM25;`--hybrid` 报错指配置;**其余一切命令与 v0.7.0 逐字节一致**(红线 a) |
   | ErrEmbedderUnavailable | 连接拒绝/超时/非 2xx | rebuild 整批失败不落半截(vecshard 单对象要么成要么不写);查询路径报错并提示稍后 |
   | ErrEmbedderModelMismatch | vecroot.model_id ≠ Embedder.ModelID() | **响亮报错要求 `kb vec rebuild`,绝不静默降级**(红线 c);报错含两侧 model_id |
   | ErrEmbedderDimMismatch | 响应向量长度 ≠ vecroot.dim | 同上——模型服务端行为变了,响亮拒绝 |
   | (超长输入) | 嵌入文本超模型上下文 | cas-kb **客户端先响亮拒绝**(不静默依赖服务端 truncate)——截断策略是显式演进项;服务端 `truncate=false` 报错作为第二道闸 |
3. **嵌入文本模板(确定性)**:`title + "\n" + ("tags: " + join(tags, ", ") + "\n" 若有标签) + body`;查询文本原样。模板字符串进 DESIGN 文档;模板版本常量(`tpl_ver`)随 model_id 一并进 vecroot 与哈希输入——红线 c 的「向量字节+model_id」的最小忠实扩展(模板变了旧向量全部作废,必须 rebuild)。
4. **超时与重试**:HTTP client 超时默认 30s(单批),失败指数退避重试 2 次(仅构建路径);查询路径不重试(交互延迟优先)。`KB_EMBED_URL` 默认空(=未配置);`KB_EMBED_MODEL` 默认 `nomic-embed-text`,中文库建议配 `bge-m3`(§2.2 表)。
5. **不采纳**:`/api/embeddings` legacy 端点(无 batch、官方已声明被取代);自管 ONNX/内嵌模型(红线 a);gRPC/自定义协议(单机 HTTP 足够)。

### 2.4 落地改动清单草案(供裁剪)

- [ ] internal/embed:Embedder 接口 + ollama 实现(/api/embed,batch ≤ 32,错误四态,超时/退避)
- [ ] KB_EMBED_URL / KB_EMBED_MODEL(+可选 KB_EMBED_TIMEOUT)进 DESIGN §8.2 配置表;凭据纪律:本机服务无凭据,未来远程服务若需 key 走环境变量名引用,不入库不入仓
- [ ] 嵌入文本模板常量化并在 DESIGN §7 记录;tpl_ver 进 vecroot
- [ ] 单元测试用 httptest 假服务钉死错误四态(不依赖真 Ollama,CI 无外部依赖)
- **不做**:`/api/embeddings` 兼容、dimensions 降维、多 embedder 并存、嵌入结果缓存层(同文本同地址,CAS 去重已覆盖)

---

## 3. 调研项 3:向量入 CAS 的先例与编码

### 3.1 cas-kb 现状

- 对象四类 + 索引两类(indexroot/indexshard,64 桶 = FNV-1a % 64,「固定片数保证同词元永远同桶」);规范编码 = JSON 字段声明序 + map 键字典序(§3.2);SQLite 索引对象透明 gzip(0x01 前缀 + gzip,≥64KB 才压,**地址/哈希基于逻辑字节**——压缩器输出不进哈希,§5.1)。
- 版本门禁两套(§7 版本规则):对象编码版本(object.SchemaVersion)与库表版本(DBSchemaVersion)各管一域;「仅追加可选字段 + omitempty 且双向兼容」两者都可不动(v5 的 `snapshot.index` 即是)。
- **schema 事实(实抓两份 DDL)**:`objects.kind` 有 CHECK 枚举(`kind IN ('blob','note','tree','snapshot','indexroot','indexshard')`)⇒ **新增 kind 必改 DDL ⇒ 库门禁必须升 v6**(「放宽 kind 约束」升 DB 版有 v5 先例)。

### 3.2 社区做法与证据

- **sqlite-vec**(Alex Garcia,Mozilla Builders 项目,README 实抓):「Store and query **float, int8, and binary vectors** in `vec0` virtual tables」「An extremely small, **"fast enough"** vector search SQLite extension」;KNN 即 `WHERE embedding MATCH ? ORDER BY distance LIMIT k`。**它证明的是:SQLite 生态内、不引入外部向量服务,平扫向量检索是成立且被广泛使用的形态**——与红线 b 同构(我们连扩展都不引,纯 Go 平扫,理由见 §4)。
- **浮点编码的一手实验**(本机 go1.26.2,脚本与输出留存于检索过程):值 -0.010071029 → float32 位型 0xbc2500f5 → LE 字节 `f5 00 25 bc`;同一 4 字节按 BE 读回 = -1.62e+32(位型 0xf50025bc)——**端序是二进制编码唯一需要约定的自由度,约定即逐字节永续**;encoding/json 输出 `[-0.010071029]`——文本形态是另一条确定性通路,但位数由「最短唯一表示」算法决定(strconv 文档实抓:precision -1 uses **「the smallest number of digits necessary such that ParseFloat will return f exactly」**),该算法是 stdlib 实现细节。**stdlib 编码输出跨版本可变有官方先例**:Go 1.8 发行注记(compress/flate 段,go.dev 实抓):「**the exact encoded output of DEFLATE may be different from Go 1.7**. Since DEFLATE is the underlying compression of gzip…those formats may have changed outputs」。
- **压缩收益的一手实验**(本机 python gzip,种子固定):模拟嵌入分布 float32 数组(高斯与单位化两种):**gzip 后均 ≈ 92%(只省 8%),与维度无关**;对照全零 3MB → 3KB(压 1000 倍)。结论:indexshard 的 −60% 先例来自文本词表高冗余,**对高熵向量不可平移**;sqlite-vec 的 binary quantization 指南(实抓)是社区对「向量压缩」的另路(1 bit/维,32× 缩减,「bound to lose a lot quality…Oversampling and re-scoring will help」)——属质量换空间的演进项,与「透明压缩」是两回事。
- **对象编码先例归纳**(只按已实抓证据立论):sqlite-vec 的 vec0 表声明支持 float/int8/binary 三种向量存储、输入示例用 JSON 文本(README 实抓;其内部存储字节形态未在本次抓取范围,不做断言);Ollama 响应用 JSON 文本(传输态)。cas-kb 选择存储态用定长二进制的依据是 §3.2 的两条一手实验(端序约定唯一自由度 + JSON 文本体积 2~3 倍与格式化算法依赖),不依赖对 sqlite-vec 内部实现的推断。

### 3.3 对 cas-kb 的裁剪建议

1. **两类新 kind,镜像 indexroot/indexshard**(红线 b 说「拟新增 kind:vecshard」,本方案按 M4 先例补一个 root,理由同 DESIGN §7 原文:独立 kind 让 fsck 的 kind 一致性校验保持精确):
   - `vecshard`(分片):`{version, model_id, tpl_ver, bucket, count, dim, vectors: [{a: note地址, v: dim×4 字节 LE float32}]}`
   - `vecroot`(根):`{version, model_id, tpl_ver, dim, count, shards[64](桶号下标,空桶空串)}`
   - **地址=哈希(向量字节+model_id)的忠实实现**:model_id 与 tpl_ver 进载荷、参与规范编码,对象地址天然绑定模型与模板(红线 c);「向量字节」按 LE 定长原样入载荷。
2. **头部 JSON 定序 + 体二进制的混合编码**:`vectors[].v` 是唯一离开 JSON 的字段(base64 或 length-prefixed 裸字节,实现择一);理由:(a) 端序约定一次,跨 Go 版本逐字节稳定(JSON 文本通路依赖 stdlib 浮点格式化算法,有 flate 式跨版本变更先例);(b) 体积约 1/2.5(JSON 文本每 float ≈ 8~12 字节 vs 4 字节);(c) 解析 O(N) 直读,无浮点 parse。
3. **确定性要点清单**:①端序 = LE(写入文档与编码注释);②float32(嵌入模型原生输出即 32 位;Ollama 响应双精度 JSON 转 float32 的舍入路径要钉死——ParseFloat(s,32) 单步到位,实验已验证位型一致);③vectors 按 note 地址**升序**(canonical 字节序的一部分,同输入必同地址);④余弦计算遍历序 = 地址升序(查询结果位型可复现,红线 c 的「同快照+同模型」);⑤gzip 沿 0x01 透明容器机制照常可用(地址按逻辑字节,压缩器版本无关),但**预期收益 ≈ 8%,可开可不开**。
4. **快照挂钩**:`snapshot.vec`(可选字段,vecroot 地址,omitempty)——沿 v5 `snapshot.index` 先例,**对象编码版本不需要动**(双向兼容的 omitempty 演进);库门禁因 kind CHECK 升 **v6**(拒绝旧库打开,指引备份/重建,沿 §8.3 口径)。
5. **体积估算(纯向量字节,float32 LE;片 = ÷64)**:

   | N(条) | 384 维(1,536B/条) | 768 维(3,072B/条) | 1024 维(4,096B/条) | 每片(768 维) |
   |---|---|---|---|---|
   | 2,000 | 3.1MB | 6.1MB | 8.2MB | 96KB |
   | 10,000 | 15.4MB | 30.7MB | 41.0MB | 480KB |
   | 100,000 | 153.6MB | 307.2MB | 410.0MB | 4.8MB |

   对比实测参照:2000 条 bulk 全库 11.1MB——768 维向量会让**当前快照**的向量索引达到正文数据的同量级;历史体积靠结构共享(单条更新只重写其所在 1 片 ≈ 全量 1/64,与词法索引「单条写≈全索引」的写放大形成数量级改善,因为一条笔记的向量只落一个桶,不像词元散布 64 桶)。

### 3.4 落地改动清单草案(供裁剪)

- [ ] schema.sql + schema_sqlite.sql:kind CHECK 增 `vecroot`/`vecshard`;版本播种 5→6;头注释记 v6 变更(沿 v5 注释形态)
- [ ] internal/object:两对象编解码 + 校验(count×dim×4 = 字节数、地址升序、model_id/tpl_ver 非空);单元测试:同输入逐字节同地址、端序/数量/dim 校验响亮
- [ ] snapshot 增 `vec` omitempty;DecodeSnapshot 双向兼容测试(旧快照字节不变)
- [ ] fsck:vec 引用一致性(vecroot→shards→note 地址存在性、dim 一致);backup/restore/pull/GC 零改动继承验证(对象层自动覆盖)
- [ ] DESIGN §3 对象表加两行;§4.1 版本约定补 v6;§7 增「向量索引(M6)」小节
- **不做**:int8/binary 量化存储(sqlite-vec 有此形态,列演进)、float16(无 stdlib 直达,收益 2× 不值复杂度)、纯 JSON 数值编码(§3.3-2 理由)

---

## 4. 调研项 4:平扫 vs ANN 的规模边界

### 4.1 cas-kb 现状

- 现有量级判断(DESIGN §7 三难定论):方案目标 ≤ 十万条,「精确遍历/全量分片优于 ANN(可复现 + 结构共享 + 双后端对称),勿提前优化」;实测基线:2000 条 search 46-58ms。向量检索若沿用「精确遍历」,该论证需要自己的数字。

### 4.2 一手基准 + 社区证据

**本机纯 Go 平扫余弦基准**(Apple M4 / go1.26.2 / 朴素逐元素点积、无 SIMD intrinsic / 预归一化点积即余弦 / 3 轮取最优,脚本与输出留存于检索过程):

| N × D | 单查询平扫耗时 | N × D | 单查询平扫耗时 |
|---|---|---|---|
| 1k × 384 | 140µs | 1k × 768 | 180µs |
| 5k × 384 | 467µs | 5k × 768 | 0.91ms |
| 10k × 384 | 0.94ms | 10k × 768 | **1.8ms** |
| 50k × 384 | 4.7ms | 50k × 768 | 9.0ms |
| — | — | 10k × 1024 | 2.4ms |
| — | — | 50k × 1024 | 11.9ms |
| — | — | 100k × 1024 | **24.1ms** |

**社区锚点**(均为实抓原文):

- sqlite-vec 作者 asg017(issue #25,2024-06-21):「`sqlite-vec` as of `v0.1.0` will be **brute-force search only**, which slows down on large datasets (**>1M** w/ large dimensions)」;ANN 计划 IVF 先行、DiskANN 后继,且「I'm wary of DiskANN because … **writes can be pretty slow** due to the internal pruning process」。该 tracking issue 至今 open(ANN PR #273 已开)——**生态头部扩展在百万级以下都靠平扫活着**。
- lsorber(同 issue 评论,Colab NumPy 实测):250k × 1024 matmul = **85ms**;「Assuming retrieval should take no longer than 100ms, the brute force approach can handle about **250k embeddings** on commodity hardware」;「binary embeddings … help to scale brute force with another factor of 2 … For that, we'd need an ANN index」。
- conradev(同 issue):「Brute force search works great in LanceDB because it uses a **contiguous columnar format**. In SQLite there is an upper bound … because the data is inherently fragmented」——**装载/连续性才是平扫的真瓶颈**,与本文基准结论一致(CAS 分片读出是 cas-kb 的对应开销)。
- **HNSW 为何不适合 CAS 冻结分片**(hnswlib README 实抓,API 形态即证据):`init_index(max_elements, M=16, ef_construction=200)` 容量可扩(`resize_index`);`add_items` **增量插入并改写邻居关系**;删除是 `mark_deleted`/`unmark_deleted` **墓碑标记**(非物理移除);`set_ef` 查询参数「currently not saved along with the index」。归纳:图索引的边依赖插入顺序与全局导航结构,**不存在「未动子结构地址不变」的分解**——CAS 冻结分片需要「改 1 条 ⇒ 重写常数个小对象、其余地址复用」(indexshard 的结构共享),HNSW 改 1 条 ⇒ 图局部失效 ⇒ 冻结快照下只能整图重建,写放大回到 O(全索引) 且结构共享归零。**这是路线级否决,不是参数级**:红线 b 的「64 桶 + 平扫」是 CAS 语义下唯一自洽形态。
- **边界判定(对 cas-kb)**:纯计算 50ms 预算 ≈ 25 万×768(本机 5 万×768=9ms 线性外推);计入 CAS 读出+解码(10k×768 = 30.7MB/查询,页缓存下毫秒级)+ 64 片逐片装载,**安全线 = 数万条,十万条进入 50~100ms 区间**——恰在 DESIGN「≤ 十万条」量级内、交互可用线(100ms,§7.2 指标 1 定性线)边缘。结论:**平扫够用到十万条;兜底靠观测挂钩,不靠预优化**。

### 4.3 对 cas-kb 的裁剪建议

1. **v1 = 64 桶 vecshard + 全片平扫**,不做任何 ANN。诚实声明:分桶不减少扫描总量(余弦没有「词→桶」的定位键,每次查询必须读全部分片),分桶买的是:①写放大 ÷64(单条更新只重写所在片);②装载粒度(逐片流式,内存峰值受控);③pull 增量传输按需取片。
2. **观测挂钩(接 §7.2)**:指标 6「检索延迟 P95(含 --at)」增补一行——「**混合检索延迟 P95:平扫段与嵌入调用段分开计时**」;触发线:**平扫段 P95 > 50ms 且条目数趋势增长**(50ms = 本机十万条×768 计算量的 2 倍余量;嵌入段是外部服务成本,单独记、不入本库决策)。触发后的演进项(按序):float32→binary 量化(sqlite-vec 32× 先例,质量损失必须过 §5 评测集)→ 进程内 IVF(需训练,破坏「确定性无训练」纪律,最后考虑)。
3. **被否方案记录**(沿 §7「三难定论」文风):进程内 HNSW——与 CAS 冻结分片语义冲突(§4.2),否;外部向量数据库——红线 b,否;嵌入服务端聚合检索——嵌入服务退化为向量库、契约复杂化,红线 a 精神,否。

### 4.4 落地改动清单草案(供裁剪)

- [ ] DESIGN §7.2 指标表增「混合检索延迟 P95」行(口径:平扫段/嵌入段分开;采集点:压测脚本;触发线:平扫段 > 50ms 且条目数趋势增长)
- [ ] 平扫实现按地址升序遍历(位型可复现,§3.3-4);逐片流式装载(峰值内存 = 单片)
- [ ] 本基准脚本归档 scripts/(压测/观测工具,不入 verify 门禁,沿 §7.2 纪律),机器与参数写注释
- **不做**:ANN、量化、缓存层;触发线未实测命中前不立项任何加速

---

## 5. 调研项 5:检索质量与评测集

### 5.1 cas-kb 现状

- 无检索质量评测:现有测试钉死的是**确定性**(同输入同输出)与契约(search --json 结构),不是**相关性**。M4 的「结果确定性可复现」是工程验收;语义增强的有效性验收需要相关性标注。
- 可复用资产:bulk import(评测语料灌注)、`--at`(评测集版本钉死)、SQLite 临时库 + e2e 脚本范式(评测脚本形态现成)。

### 5.2 社区惯例与证据

- **BEIR**(实抓摘要):零样本评测范式 = 多数据集 + 统一指标(nDCG@10/recall@k)+ **词法基线必须同场**;「BM25 is a robust baseline」是一切语义检索的第一道验收。
- **Qdrant 调参文章**(实抓):「**Confirm Fusion Beats Either Prefetch Before tuning** … Compare dense retrieval, sparse retrieval, and default Reciprocal Rank Fusion … **Score all three with nDCG@10**」;用 95% 置信区间防假增益(「its interval crosses zero」)。——混合检索验收惯例:**三路同测(BM25 / 纯向量 / 混合)、排序质量指标、防倒退优先**。
- **RRF 论文**自身即评测范式样板:TREC 多系统 + LETOR,报告相对提升而非绝对分。
- 诚实边界:以上均为大规模学术评测;20~50 条的小集只能回答「有没有效果」,回答不了「效果好多少」——这个限制写进验收报告模板,不掩饰。

### 5.3 对 cas-kb 的裁剪建议(评测集构造草案)

1. **语料**:中文知识条目 30~50 条(覆盖定义类/因果类/操作类/代码类各 ≥8 条;标题与正文长度有意拉开;含 5 条英文混入),以 `kb bulk import` 灌入临时库,**评测固定一个快照地址**(`--at` 钉死)——红线 c 的「同快照+同模型」在评测里 = 快照钉死 + 模型钉死 + 模板钉死。
2. **查询与标注**:15~25 条查询,每查询人工标相关条目 1~3 条(qrels 文件);查询分三类,**配额固定**:
   - **词面命中型(约 8 条)**:查询词直接出现在目标条目——BM25 本该赢,验收「不倒退」;
   - **同义改写型(约 8 条)**:查询与目标条目**零词面重叠**(如条目「goroutine 泄漏排查」↔ 查询「协程内存不释放怎么办」)——语义该赢,验收「增益」;
   - **概念联想型(约 4~8 条)**:相关但弱关联(如「channel 死锁」↔「context 超时取消」)——灰区,只观测不计验收线。
3. **指标与判定**:主指标 recall@5(三路同测:纯 BM25 / 纯向量 / 混合);nDCG@10 可选、不作验收。
4. **三条验收线(草案,合入前必须全绿)**:
   - **不倒退线**:词面型子集,混合 recall@5 ≥ 纯 BM25 recall@5(逐查询不允许更低——RRF 的 1/(60+rank) 保底使混合在词面强查询上几乎不损,跌破即实现有错);
   - **增益线**:改写型子集,混合 recall@5 相对纯 BM25 的提升 ≥ 预登记绝对值(基线固定后校准,建议起点:**8 条改写查询合计至少多召回 3 条**;若 BM25 意外已能命中改写查询,换用「纯向量 recall@5 ≥ BM25 + 同值」的等效判据并在报告说明);
   - **可复现线**:同快照+同模型+同模板,两次全量 rebuild 后 vecroot 地址一致;同一查询两次检索结果序列逐字节一致。
5. **形态**:`scripts/eval-search.sh`(脚本优先,零产品面)——临时库 + 真本机 Ollama 或服务桩,TAP 式逐行 ok/not ok + 汇总(沿 drill 脚本纪律);**默认不进 verify 门禁**(`EVAL=1` 选择性跑);语料与 qrels 放 `scripts/evaldata/`(文本文件入库,库文件不入库);评测报告写回 docs/review/(新文件,与本文互不影响)。

### 5.4 落地改动清单草案(供裁剪)

- [ ] 评测语料 + qrels 文件(含构造说明:三类查询配额、标注规则)
- [ ] 评测脚本(TAP 式,三路同测,退出码=失败数,临时库,EVAL=1 接入 verify)
- [ ] 验收报告模板(含「小样本冒烟级,非学术结论」声明与基线校准记录位)
- **不做**:大规模语料、显著性检验、LLM-as-judge(标注自动化可作远期演进,本期人工标注量 ≤ 150 条可承受)

---

## 6. M6 实施清单(分批与验收标准草案,供负责人直接立项)

| 批次 | 项 | 内容 | 验收标准草案(可执行) | 依赖 |
|---|---|---|---|---|
| **A** | A1 schema v6 | kind CHECK 增 vecroot/vecshard;播种 5→6;两份 DDL 同步;旧库拒绝指引 | `grep -c vecshard schema.sql schema_sqlite.sql` 各 ≥2(CHECK+注释);打开 v5 库报错含「重建」指引(测试钉死) | 无 |
| **A** | A2 对象编解码 | vecshard/vecroot 编解码(§3.3-1 形态);校验与确定性 | `go test ./internal/object/ -run TestVec`:同输入逐字节同地址;端序/数量/dim 校验响亮;地址升序断言 | A1 |
| **A** | A3 快照挂钩 | `snapshot.vec`(omitempty);fsck 扩展;backup/pull/GC 继承验证 | 旧快照编码字节不变(双向兼容测试);含 vec 快照 fsck 过、可 pull/backup roundtrip | A2 |
| **A** | A4 Embedder | 接口 + Ollama /api/embed 实现;KB_EMBED_URL/KB_EMBED_MODEL 入 §8.2;错误四态 | httptest 假服务矩阵:未配置=v0.7 行为、不可达、模型/维度不匹配四态各自响亮;`go test ./internal/embed/` 全绿 | 无(与 A1~A3 并行) |
| **A** | A5 kb vec rebuild | 从当前快照全量构建(batch 嵌入→64 片→vecroot→snapshot.vec);失败不落半截 | 同快照+同模型+同模板两次 rebuild ⇒ vecroot 地址一致(幂等);嵌入服务中途断 ⇒ 无 vec 对象残留、分支指针不动;**未配置嵌入服务时全部既有测试逐字节不变** | A2~A4 |
| **B** | B1 混合检索 | `kb search --hybrid`:RRF k=60、top-W=max(50,5n)、tie-break 沿 M4;失败三态(§1.3-5) | `go test ./cmd/kb/ -run TestSearchHybrid`:无 vecroot/模型不匹配/未配置各自响亮文案;不带 --hybrid 输出与 v0.7.0 逐字节一致 | A5 |
| **B** | B2 HTTP + 契约 | `GET /api/v1/search` 增 `hybrid=1`(仅字面 1 生效);view 契约扩展;parity 钉死 | TestServeCLIParity 扩展:CLI --json 与 HTTP 同字段同序;无旗标响应与 v0.7.0 逐字节一致 | B1 |
| **B** | B3 doctor 检查项 | `embed` 检查项追加表尾:配置形态/服务可达/model_id 与 vecroot 一致性 | `kb doctor --list-checks` 含 embed 且旧检查名不变;嵌入不可达=warn 不拦退出码(沿 doctor 克制纪律) | A4 |
| **B** | B4 评测与观测 | 评测集+脚本(§5.3);§7.2 指标 6 增行;基准脚本归档 | 评测三线全绿(不倒退/增益/可复现)并出报告;`EVAL=1 ./scripts/verify.sh` 可选跑通;默认门禁时长无感 | B1 |
| **B** | B5 文档四处 | DESIGN §7 增「向量索引(M6)」小节(公式/编码/失败语义/IVF 旧句改判);ROADMAP M6 行;README 检索段;CHANGELOG | 文档交叉引用一致(§3/§4/§7/§8.2 互指);ROADMAP 顶部状态行更新 | 全部 |

**分批理由**:A 批是纯对象层(编码/门禁/rebuild),不碰检索行为,未配置嵌入服务时对用户完全不可见——红线 a 的验收在 A 批即可闭合;B 批才让行为可见(--hybrid/HTTP/doctor),评测兜底「语义真的有效」。A 可独立交付发版(存储能力先行),B 依赖 A5 的 vecroot 但不依赖其规模化。

---

## 7. 证据与检索方法附注(诚实清单)

- 全部外部链接为 2026-09-02~03 实抓(curl 或等价 HTTP GET);关键断言尽量「官方文档 + 一手实验」双证据:RRF 公式取自论文 PDF 正文抽取(非转述),Ollama 契约取自官方 api.md,模型参数取自 Ollama registry API 与 HF config.json,浮点/gzip 结论来自本机可复现实验(种子固定)。
- **检索不到/未采信的项,如实记录**:
  - **本机无 Ollama 运行**:127.0.0.1:11434 的 /api/tags、/api/embeddings、/api/version 三路探测均无响应——§2 契约以官方 api.md 为准,**未经本机实测**;落地 A4 时应先在本机补一次真实往返验证。
  - **huggingface.co 直连超时**(curl 与 fetch 双路失败):模型卡与 config.json 经 **hf-mirror.com 镜像**取得(同路径同内容,镜像站非官方);bge-m3 max_position_embeddings=8194 等数字来自镜像抓取。
  - **ollama.com 模型页为 JS 渲染**,文本抓取只得一句描述;维度/上下文参数改由 registry.ollama.ai manifest + config/params blob(一手)与 HF config.json(镜像)补齐。**nomic-embed-text:latest 与 v1.5 tag 的对应关系、latest 的 num_ctx** 未从官方核实(只实测 v1.5 tag params = {"num_ctx":8192} 与 latest config blob 的 137M/nomic-bert)。
  - **Weaviate「alpha 默认 0.75」的流传说法未采信**:官方文档页实抓只见示例值 0.5/0.25,未抓到默认值原文;本文只断言「文档示例 0.5」。
  - **`/api/embed` 的 `dimensions` 参数**(MRL 式降维)对具体模型的支持程度未核实(api.md 只声明参数存在);cas-kb 方案不使用该参数,不受影响。
  - **Go strconv 浮点格式化算法的历史变更**(如最短表示算法重写)未在 go.dev 发行注记实抓到原文段;浮点文本化风险改以两点立据:Go 1.8 compress/flate 官方声明(输出跨版本可变,已实抓)+ 本机 JSON/LE 对照实验。
  - **HNSW 原始论文**(Malkov & Yashunin)未抓取;HNSW 可变性论证以 hnswlib README 的 API 形态(add_items/mark_deleted/resize_index/set_ef,已实抓)为准。
  - **lsorber 的 250k×1024=85ms** 是 GitHub issue 评论(Google Colab + NumPy),社区数据点非官方基准;cas-kb 场景边界判定以本机 Go 基准(一手)为准,该数字只作跨环境旁证。
  - **BEIR「BM25 在 18 数据集中 11 项胜过 dense」的具体计数**未核实(本次只实抓摘要,未取正文表格);正文只引用摘要原句。
  - **Qdrant「k=2 默认」**出处为其官方博客文章(2026-08-22,已实抓),非 API 文档页;其公式与论文 k 的映射(k=2 → 论文 61)按该文转述。
  - sqlite-vec 的 **ANN 支持状态**:tracking issue #25 自 2024-06-21 open 未关、ANN PR #273 已开(该提交存在已实抓);是否已随某个 release 发布未核实,本文以「截至检索日 brute-force 为主形态」为限。
- 本文件为 T53 唯一交付物;未修改任何代码与既有文档;不推送、不合并。

---

## 8. 附记(提交时环境观察,2026-09-03)

- 本调研基于 v0.7.0(099d55e)撰写;提交时发现本仓库 main 已推进至 **v0.8.0(40ead22):M6-A(向量对象模型,schema v6)与 M6-B(混合检索 + 评测集)已实现合并**,DESIGN §7.3 标注「已交付」,评测语料落 `tests/eval/`——实现取舍与本文 §6 清单同向,细节以 v0.8.0 代码为准(旗标/命令命名:实现为 `--hybrid` + `mode=hybrid`、`rebuild --embed`,与本文草案的 `hybrid=1`/`kb vec rebuild` 命名不同;RRF k=60 固定、失败三哨兵、同快照+同 model_id 确定性边界、评测集「语义查询 recall@5 严格优于纯 BM25」等核心结论被采纳,见 v0.8.0 提交 ea120f9/0f1243a 的提交说明)。
- 原 worktree(cas-kb-t53)与分支 research/vector-search 在 v0.8.0 合并后被例行清理,本文档因当时未提交而不在 main 树中(main 的 docs/research/ 无本文件,已核实);本次提交按原任务口径在 099d55e 重建分支与 worktree 后落盘。**本文件内容仍为 v0.7.0 时点的调研证据**;若后续合并,属纯文档增量,与 v0.8.0 已交付实现无代码冲突,可作 §6 清单与 §7 证据链的决策档案。
