# 面向「AI 维护、AI 消费」的知识库:产品实现逻辑调研

> 调研时间:2026-09-01 · 方法:四路并行子代理检索 + 一手论文/官方文档交叉核验(Mem0 arXiv:2504.19413、Zep arXiv:2501.13956 等)
> 用途:为 cas-kb(内容寻址 + Merkle 知识库)的产品定位与 M4+ 规划提供参考
> 原则:查不到的内部实现一律标注「未公开」,不做推测;各家论文数字均为自评基准,横向仅供量级参考

## 0. TL;DR

1. 这类产品的本质是**把知识的生产者和消费者都换成 AI**:写路径靠 LLM 抽取/合并自动化,读路径适配上下文窗口与注意力经济,治理必须机器可校验(审计/回滚/作用域/防投毒)。
2. 业界收敛出三条产品路线:**A. 记忆层平台**(Mem0/Zep/Letta/LangMem/Cognee/Vertex Memory Bank/memOS/Supermemory,「事件流→抽取→合并→检索 API」)、**B. 文件即知识库**(CLAUDE.md/AGENTS.md/Cursor rules/Windsurf/Devin/OpenHands/Manus,git 管理 Markdown)、**C. 助手个性化记忆**(ChatGPT/Claude/Gemini,平台托管画像注入)。
3. 关键分野在**写侧合并机制**:Mem0 用 LLM 四操作(ADD/UPDATE/DELETE/NOOP);Zep 双时态「只失效不删除」;Letta 让 Agent 自编辑 + 后台 dreaming 整理;LangMem 分热路径/后台双模式;编码智能体则以「人工 git 文件为主、自动生成兜底」。
4. 读侧两种范式:**会话启动自动注入**(文件路线,无检索)与**显式检索 API**(记忆平台,混合检索+重排);自动注入派生出截断注入、渐进披露、KV-cache 前缀稳定等 token 经济学技巧。
5. 检索底座:图增强三派(GraphRAG 贵而深、LightRAG 增量便宜、HippoRAG PPR 多跳)+ 时序知识图(唯一原生解决「过期事实」的路线)。
6. **普遍缺口:知识写入缺乏可证明的完整性、版本化与可回滚**。反向信号:Letta 的记忆就是「每个 Agent 一个 git 仓库」,Supermemory 按「唯一内容」计费(重复免计),memU 把记忆组织成文件系统——**知识库的 git 化/文件化/去重化已被业界侧面验证,但无一家做到 Merkle 级完整性证明**。这正是 cas-kb 的差异化空间:「记忆的 git 化」。

## 1. 场景定义:与 RAG 知识库、人类 Wiki 的区别

| 维度 | 人类 Wiki/文档库 | 传统 RAG 知识库 | AI 维护+AI 消费知识库 |
|---|---|---|---|
| 作者是谁 | 人(专职/众包) | 人(上传文档) | AI 从交互流自动沉淀 + 人工兜底 |
| 读者是谁 | 人 | AI(检索增强) | AI(检索/注入) |
| 写入触发 | 计划性编辑 | 批量导入 | 对话热路径、会话结束后台任务、被纠正时、人工 commit |
| 一致性维护 | 编辑评审 | 无(文档静态) | LLM 合并/冲突判定/时效失效,知识持续演化 |
| 治理诉求 | 权限+流程 | 版权+新鲜度 | 审计溯源、回滚、作用域隔离、防投毒、遗忘(GDPR) |
| 读取形态 | 浏览搜索 | 检索 top-k 注入 | 自动注入 / 工具检索 / 宏直引,token 预算极小 |

核心新问题:**知识在持续演化**(事实过期、互相矛盾)、**写入者不可完全信任**(LLM 幻觉、提示注入)、**读取预算按 token 计价**。

## 2. 三条产品路线全景

### 路线 A:通用记忆层平台(对话/事件流 → 知识)

| 产品 | 存储模型 | 写路径 | 读路径 | 独特机制 |
|---|---|---|---|---|
| **Mem0** | 原子事实文本+稠密向量;变更历史库;图变体 Mem0g(Neo4j/Memgraph) | 两阶段:抽候选事实 → 召回相似旧记忆 → LLM 四操作 tool-call(ADD/UPDATE/DELETE/NOOP),天然去重/覆盖 | 显式 API search,按 user_id/agent_id/run_id 过滤;平台版可选 rerank/category | LOCOMO 比 OpenAI 记忆相对 +26%,p95 -91%,token -90%([arXiv:2504.19413](https://arxiv.org/abs/2504.19413)) |
| **Zep / Graphiti** | 时序属性图:episodes(无损原文)+实体节点+语义边;Neo4j/FalkorDB | Episode 流入增量抽取;实体消歧;矛盾边**置 invalid 不删**:valid_at/invalid_at(事件时间)+created_at/expired_at(事务时间)双时态;增量社区检测 | 混合检索=余弦+BM25+图 BFS 三路召回,RRF/MMR 重排;支持按 valid_at 时间窗过滤 | DMR 94.8% vs MemGPT 93.4%;LongMemEval 最高 +18.5%,延迟 -90%([arXiv:2501.13956](https://arxiv.org/abs/2501.13956)) |
| **Letta (MemGPT)** | 内存块常驻 + MemFS:**每个 Agent 一份 git 仓库作记忆**,system/ 下文件每轮进系统提示;档案向量库 | Agent 运行中工具**自编辑**内存块;MemFS 编辑经 git commit/push 生效,可多 Agent 共享 | 内存块自动在上下文;文件树+检索按需读;状态跨会话跨模型迁移 | sleep-time compute(dreaming):后台子 Agent 复盘对话把教训合并进共享内存块([docs](https://docs.letta.com/agent-sdk/memory/index.md)、[arXiv:2504.13171](https://arxiv.org/abs/2504.13171)) |
| **LangMem / LangGraph Store** | namespace 分层键值存储,值可内嵌向量;语义/情景/程序三类记忆 | 双模式:hot path 工具即时写 vs 后台 ReflectionExecutor 异步抽取+整合 | create_search_memory_tool 显式检索,开发者自行接线 | 把「何时形成记忆、形成什么」方法论化([langmem](https://langchain-ai.github.io/langmem/)) |
| **Cognee** | 图+向量双引擎,30+ 适配器;可配本体(ontology)约束 | ECL 管线:add→cognify(分块→抽三元组建图→本体对齐);重跑产新版本节点 | search 多类型:图补全/RAG/摘要;user/tenant 隔离+数据血缘 | 本体驱动抽取;开源「认知层」([docs](https://docs.cognee.ai)) |
| **Vertex AI Memory Bank** | 全托管黑盒:Gemini 抽事实+Google 嵌入索引 | generate_memories 持续更新;自动去重覆盖(改口「喜欢红色」自动替换「蓝色」);TTL 过期;topics 控制抽取范围 | ADK 自动召回注入;独立 Agent 用工具显式拉取 | 与 Agent Engine 会话托管一体化;topics 粒度「教 Agent 记什么」([FAQ](https://discuss.google.dev/t/lets-talk-about-vertex-ai-memory-bank-heres-a-quick-faq-on-the-top-5-questions-weve-seen/244685)) |
| **memOS / memU** | memOS:MemCube 统一明文/激活(KV-cache)/参数化三态;memU:记忆=分层文件系统(类别=文件夹、记忆=文件、交叉引用=符号链接、原始对话=挂载点;PostgreSQL+pgvector) | memU:7x24 后台记忆 Agent 持续抽取,按显著度(salience)整理 | 链接直取+向量双路 | 「记忆操作系统」([arXiv:2507.03724](https://arxiv.org/abs/2507.03724));memU 记忆文件树([GitHub](https://github.com/NevaMind-AI/memU)) |
| **Supermemory** | 自研向量+图:Memory Graph 带本体感知边(更新/合并/矛盾标记);画像层+连接器(Notion/GDrive/S3/Gmail) | POST documents 入档任意格式,智能分块抽取;图边合并引擎自动做 | 混合检索+上下文感知重排+图遍历,p50 小于 300ms;MCP/IDE 按需注入;Infinite Chat | **按唯一内容计费(重复内容免计)**;LongMemEval 85.2%/LoCoMo 榜首(自报)([docs](https://docs.supermemory.ai)) |

### 路线 B:编码智能体的知识库(文件即知识库)

| 产品 | 存储 | 写入 | 读取 | 独特机制 |
|---|---|---|---|---|
| **Claude Code** | 纯 Markdown:CLAUDE.md 四层(组织策略→用户 ~/.claude→项目→CLAUDE.local.md)+ .claude/rules/ + auto memory;API 端另有客户端托管 /memories 目录(memory tool) | 人工 git 维护(# 键追加);auto memory 被纠正时自动记录 | 会话启动按 组织→用户→项目→本地 顺序自动注入;auto memory 只注入前 200 行/25KB | 组织策略层(IT 管控);官方明言「记忆只是上下文非强制」,硬约束须 PreToolUse hook;大文件截断注入([文档](https://code.claude.com/docs/en/memory)) |
| **Cursor** | .cursor/rules/*.mdc(frontmatter)+ User/Team Rules + 服务端 Memories(未公开) | Rules 全人工;Memories 对话自动生成,可编辑/删除 | 置于上下文开头;frontmatter 四种触发:恒注入/模型按 description 选取/glob 命中附加/仅 @引用 | 单文件表达四种注入策略([文档](https://cursor.com/docs/rules)) |
| **Windsurf** | Memories(Cascade 自动生成,工作区隔离)+ Rules(单文件上限 12k 字符;引擎未公开) | 自动生成或主动「create a memory of…」 | 按相关性自动取回;规则沿 git root 向上级联发现、就近去重;绑 glob 或自然语言触发 | 记忆/规则同一面板人工可审计([文档](https://docs.devin.ai/windsurf/plugins/cascade/memories)) |
| **Devin** | 云端:Knowledge(触发描述+内容)、Playbooks、DeepWiki | 手动建;Devin 自动**建议**、人工确认入库 | 按 Trigger Description 相关性召回;prompt 里 !macro 直引 | 触发器+宏双寻址;组织共享与个人开关分离(禁用不等于删除)([文档](https://docs.devin.ai/product-guides/knowledge)) |
| **OpenHands** | repo 内 AGENTS.md + .agents/skills/SKILL.md,零服务端 | 全人工,git+PR 即知识评审 | 三级渐进披露:目录(name+description)→匹配才加载全文→资源按需读;keyword/paths 双触发器 | 渐进披露控 token([文档](https://docs.openhands.dev/usage/prompting/microagents-overview)) |
| **AGENTS.md** | repo 根 Markdown,零 schema,嵌套就近优先 | 人工,git+PR | 启动时全文注入,无检索 | 一文件通吃多家工具;60k+ 项目采用,2025-12 入 Linux 基金会([agents.md](https://agents.md)) |
| **Manus** | 文件系统=磁盘,上下文=RAM;工具结果全量落盘,上下文只留指针 | 模型自建自改 todo.md;压缩必保可恢复指针 | 反复重读 todo.md 把目标推回注意力末尾(recitation) | KV-cache 命中率列为第一生产指标(输入输出比约 100:1,缓存价差 10x);append-only、失败轨迹保留([博客](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus)) |

**路线 B 共性**:写入「人工 git 文件为主、自动生成兜底」;读取全靠「启动自动注入+按需加载」,**向量检索/重排几乎缺位**;团队共享走 git(本地派)或组织云端面板(SaaS 派)。

### 路线 C:对话助手个性化记忆(平台托管)

- **ChatGPT**:双轨——saved memories(bio 工具写入的带日期事实条目)+ reference chat history(后台离线生成的用户画像)。**均非向量库,纯文本注入系统提示**(Model Set Context 五节:回复偏好/话题亮点/用户洞察带 Confidence/约 40 条用户原话/交互元数据)。画像不可见不可编辑;bio 工具可被间接提示注入滥用;Settings 可删条目、可关画像。
- **Claude 应用端**:可编辑的主题记忆文件(Settings→Memory→Topics),蒸馏笔记而非对话索引;敏感主题默认排除;**opt-in、每 Project 独立记忆、管理员可禁用、可导出**。开发者平台另有客户端 memory tool(/memories 文件)。
- **Gemini**:Saved Info 显式条目 + Personal Context 聊天画像;检索旧对话且**引用时标注出处**;Temporary Chat 不进历史不喂画像。

**路线 C 共性**:都不是 RAG 式对话索引,而是「事实/画像文本注入」;隐私姿态分化:ChatGPT 最激进(隐形画像),Claude 最保守(opt-in+隔离+可导出),Gemini 折中(显式条目+带出处引用)。抽取/冲突更新内部实现均**未公开**。

## 3. 实现逻辑的共性抽象(参考架构)

### 3.1 写路径(知识如何被 AI 维护)

~~~text
交互事件流(对话/工具调用/纠错)
  → 触发器(热路径 | 会话结束后台 | 被纠正时 | 人工 commit)
  → LLM 抽取候选事实(带 rolling summary、salience 评分)
  → 召回相似已有知识(向量/图)
  → 合并决策:LLM 四操作 ADD/UPDATE/DELETE/NOOP(Mem0)
              或 双时态失效旧边+新边(Zep)
              或 建议后人工确认(Devin)
  → 带溯源入库(指回原始事件)
~~~

要点:①**合并决策本身就是一次 LLM 调用**,是各家工程质量差异的核心;②**删除普遍被弱化为失效/停用**(Zep 只失效、Devin 禁用不等于删除),保留历史以支持时序推理与审计;③人工门(Devin、组织策略层)是防投毒第一道闸。

### 3.2 读路径(知识如何被 AI 消费)

| 范式 | 机制 | 代表 | 适用 |
|---|---|---|---|
| 自动注入 | 启动时全文/分层加载进系统提示 | CLAUDE.md、AGENTS.md、Letta system/ | 稳定、小体积、高价值指令 |
| 截断注入 | 大文件只注入头部(200 行/25KB) | Claude Code auto memory | 防上下文爆仓 |
| 相关时插入 | 模型按 description 选取 / glob 命中 | Cursor rules、Windsurf | 主题化规则 |
| 显式检索 | API/工具调用,混合检索+重排 | Mem0、Zep、memory tool | 大体量、演化型知识 |
| 宏直引 | !macro 确定性寻址 | Devin、OpenHands paths | 流程模板 |
| 渐进披露 | 目录→全文→资源三级加载 | OpenHands skills、技能生态 | 大规模技能库 |
| 指针代替正文 | 工具结果落盘,上下文留路径 | Manus | 执行型长轨迹 |

检索栈标准做法:**BM25 + 向量并行召回 → RRF 融合(k=60,免调权)→ cross-encoder 重排 top-N(几十到 100 个候选,约 100-200ms)**。

图增强三派(均为显式工具调用式查询):
- **GraphRAG**:实体图+Leiden 社区+多层级摘要;Local/Global/DRIFT(2025,局部起点递归展开)三查询;索引对每 chunk 多轮 LLM 调用,**最贵**,适合小而深的静态语料全局分析([索引文档](https://github.com/microsoft/graphrag/blob/main/docs/index/overview.md))。
- **LightRAG**:low-level/high-level 双层关键词检索;增量合并新片段、不整图重建;成本被第三方评述为「不到 GraphRAG 的 1/100」(精确倍数随数据集变化);EMNLP 2025([arXiv:2410.05779](https://arxiv.org/abs/2410.05779))。
- **HippoRAG 1/2**:OpenIE 三元组构图,**原文段落也作图节点**;query 实体作 PPR 种子单步扩散;多跳检索超 SOTA 最高 20%,单步比 IRCoT 迭代检索**便宜 10-20x、快 6-13x**;HippoRAG 2(「From RAG to Memory」)还证实 RAPTOR 类 LLM 摘要式索引会向检索库注入噪声导致退化([arXiv:2405.14831](https://arxiv.org/abs/2405.14831)、[arXiv:2502.14802](https://arxiv.org/abs/2502.14802))。
- 时序知识图(Zep Graphiti、T-GRAG):**唯一原生解「过期事实」的路线**,双时态边失效+episode 双向溯源([T-GRAG](https://dl.acm.org/doi/10.1145/3746027.3755628))。

### 3.3 治理与失效模式

- 作用域:用户/项目/工作区/组织四级是事实标准;closest-wins 就近优先。
- 遗忘:TTL(Vertex)、临时会话不入库(Gemini)、截断注入、双时态失效;业界归纳为 importance/merge/decay/eviction 四杠杆([Elastic](https://www.elastic.co/search-labs/blog/agentic-memory-management-elasticsearch))。
- 基准:**LongMemEval**(ICLR 2025,500 题,考信息抽取/多会话推理/知识更新/时序推理/拒答五种能力——商用助手在更新/时序/拒答上最弱;工程解方:时间感知索引与查询扩展、key 扩充,[arXiv:2410.10813](https://arxiv.org/abs/2410.10813));**LoCoMo**(ACL 2024,约 300 轮多会话含多模态,长上下文与 RAG 策略均远低于人类,[arXiv:2402.17753](https://arxiv.org/abs/2402.17753))。
- 已知失效模式:**记忆投毒**(MemoryGraft 把恶意「成功经验」植入长期记忆,借经验检索通道持久化劫持、绕过即时越狱检测,[arXiv:2512.16962](https://arxiv.org/abs/2512.16962));**过期事实**(新旧并存时 LLM 难以用记忆覆盖已内化旧知);**噪声累积**(「什么都记=什么都记不住」,无质量门的抽取使记忆库退化)。

## 4. 对 cas-kb 的定位启示

### 4.1 业界缺口 = cas-kb 的差异化空间

上述产品的知识写入几乎全部**缺少可证明的完整性与版本化**:Mem0 只有变更历史表(可查不可验);Zep 有双时态失效记录但图内容不可证伪;文件路线靠 git——人工写入场景下 git 够用,但 **Letta 已经把每个 Agent 的记忆做成 git 仓库**(自编辑经 commit/push 生效),Supermemory 按**唯一内容**计费(重复免计),memU 把记忆做成文件树——说明「知识库 git 化/去重化/文件化」方向已被验证,但**无一家做到内容寻址 + Merkle 完整性证明**。

cas-kb 天然提供:内容去重、完整性证明、可复现的知识树快照、分支即隔离的记忆工作区、合并即评审门、Merkle 证明即审计。**卖点一句话:memory-as-code——给 AI 写进知识库的每一条事实一个可验证、可回滚、可评审的 git 化工作流。**

### 4.2 可借鉴机制清单(映射到设计)

| 业界机制 | cas-kb 落点 |
|---|---|
| Mem0 四操作合并 | 抽取适配器产出候选事实 → LLM 合并决策 → 落为 branch 上的 add/update/tombstone 提交,决策记录入 DAG |
| Zep 双时态 | 节点元数据加 valid_at/invalid_at(事实时间)+ 摄入时间(branch commit 时间线天然承担);检索按时效过滤 |
| 只失效不删除 | tombstone 标记而非物理删除;物理清理走 unreferenced GC——与 CAS 不可变语义自洽;注意 GDPR 删除需显式 purge(历史分支仍可达) |
| Devin 人工门 | branch 合并 = 评审门;AI 写入默认进 staging branch,机械验收通过后 merge |
| Letta 记忆=git 仓库 | 直接印证 branches 唯一可变状态的设计;cas-kb 可提供 MemFS 式挂载视图 |
| OpenHands 渐进披露 | 检索结果只返回 blob 摘要+地址,正文按需 fetch |
| Manus 指针注入 | 检索 API 返回内容地址而非全文拼接,调用方决定加载深度 |
| 作用域隔离 | branch/user/org 三级命名空间;个人开关=按 branch 过滤 |
| Supermemory 唯一内容计费 | CAS 去重免费获得,可作成本卖点 |
| MCP 生态 | M4 的 HTTP API 之上包一层 MCP server(检索/写入工具),对齐 memory tool 形态 |

### 4.3 架构纪律(与现有设计一致)

1. **CAS 是权威存储,索引是派生数据**:BM25/向量/图索引全部可重建,坏了就 reindex;HippoRAG 2 证实 LLM 摘要式索引会注入噪声——索引可重建恰好是纠错手段,业界(vector DB 即真相)的坑可直接绕开。
2. **检索先行,抽取后行**:M4 若做检索,先服务「人写/AI 读」;抽取管线(写路径自动化)是独立增量,不应阻塞检索。
3. **每条知识必有溯源边**(指向源 blob/会话),这是与 Mem0/Zep 拉开差距的最低成本功能,也直接回应 LongMemEval 的时序推理短板与记忆投毒问题。

## 5. 主要参考

- 论文:Mem0 [arXiv:2504.19413](https://arxiv.org/abs/2504.19413) · Zep [arXiv:2501.13956](https://arxiv.org/abs/2501.13956) · MemGPT [arXiv:2310.08560](https://arxiv.org/abs/2310.08560) · Letta sleep-time compute [arXiv:2504.13171](https://arxiv.org/abs/2504.13171) · memOS [arXiv:2507.03724](https://arxiv.org/abs/2507.03724) · LightRAG [arXiv:2410.05779](https://arxiv.org/abs/2410.05779) · HippoRAG [arXiv:2405.14831](https://arxiv.org/abs/2405.14831) / [arXiv:2502.14802](https://arxiv.org/abs/2502.14802) · LongMemEval [arXiv:2410.10813](https://arxiv.org/abs/2410.10813) · LoCoMo [arXiv:2402.17753](https://arxiv.org/abs/2402.17753) · MemoryGraft [arXiv:2512.16962](https://arxiv.org/abs/2512.16962) · T-GRAG [ACM](https://dl.acm.org/doi/10.1145/3746027.3755628)
- 官方文档:[Anthropic memory tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool) · [Claude Code memory](https://code.claude.com/docs/en/memory) · [Cursor rules](https://cursor.com/docs/rules) · [Devin Knowledge](https://docs.devin.ai/product-guides/knowledge) · [OpenHands microagents](https://docs.openhands.dev/usage/prompting/microagents-overview) · [agents.md](https://agents.md) · [Windsurf Memories](https://docs.devin.ai/windsurf/plugins/cascade/memories) · [Letta memory](https://docs.letta.com/agent-sdk/memory/index.md) · [LangMem](https://langchain-ai.github.io/langmem/) · [Cognee](https://docs.cognee.ai) · [Vertex Memory Bank FAQ](https://discuss.google.dev/t/lets-talk-about-vertex-ai-memory-bank-heres-a-quick-faq-on-the-top-5-questions-weve-seen/244685) · [memU](https://github.com/NevaMind-AI/memU) · [Supermemory](https://docs.supermemory.ai) · [GraphRAG](https://github.com/microsoft/graphrag/blob/main/docs/index/overview.md) · [Graphiti](https://help.getzep.com/graphiti/getting-started/overview)
- 博客:[Manus: Context Engineering](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus) · [Elastic 记忆管理](https://www.elastic.co/search-labs/blog/agentic-memory-management-elasticsearch)
- 方法说明:调研由四路子代理(通用记忆层/编码智能体/助手记忆/检索底座)并行完成,关键结论经主会话一手来源复核。