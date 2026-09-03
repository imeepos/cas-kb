# 落地路线图

每个里程碑交付可独立验收的能力;验收标准即测试用例的来源。

> 状态:M1–M3.10 已交付并通过验收(M3.10=存储后端可插拔:SQLite 默认、PostgreSQL 可选);M3.11=dir tree 全库视图(展示层增量)已交付;M4 CLI 部分已交付(倒排索引纳入快照 + kb search / link resolve / index rebuild,schema v5);M3.12=Markdown 互操作(export md / import md,增量)已交付;M4 增补=gc --keep-last K 历史保留水位(历史索引精简)已交付;M4 收尾=只读 HTTP API(kb serve,DESIGN §8.5)已交付;M4.1=写入型 HTTP API(kb serve 写端点,令牌鉴权,DESIGN §8.6)已交付;M4.2=检索片段高亮(纯展示层增量:--snippet / snippet=1,DESIGN §7.1)已交付;M5=三方合并(pull --merge,repo 内核 A 批次 + CLI/中间态/收束 B 批次,DESIGN §6.3)已交付;T44=多端冷启动修复(pull 空远端「已是最新」空操作 + `--merge --allow-unrelated` 空基线合并 + 无共同历史文案分流,DESIGN §6.2/§6.3)已交付;M6-A=向量对象模型与嵌入重建(vecshard/vecroot + Embedder 适配器 + kb index rebuild --embed,schema v6,DESIGN §7.3)已交付;M6-B=混合检索集成(kb search --hybrid 与 /api/v1/search?mode=hybrid,RRF(k=60)融合 BM25 与向量余弦、评测集钉死语义增益与词法无损,BM25 默认不动,DESIGN §7.3)已交付;M6-C=OpenAI 兼容嵌入适配器(KB_EMBED_PROVIDER ∈ {ollama,openai} 双提供者,/v1/embeddings + Bearer 鉴权、响应按 index 对齐、key 不日志不回显,DESIGN §7.3/§8.2)已交付。

## M1 存储内核

**范围**:`hash` / `object` / `store` 三层、Postgres 迁移、CLI `kb init`。

**验收标准**
- 同一对象重复 Put,库中只有一行(幂等)
- Get 返回的字节与写入逐字节一致,kind 正确;不存在的地址返回 NotFound
- 篡改 data 后按 addr 重算哈希必不匹配(完整性可检测)
- meta.schema_version 不匹配时,打开仓库直接报错拒绝(误配置响亮失败)
- 规范编码稳定性:同一逻辑对象编码两次,字节与地址完全一致

## M2 知识条目与版本

**范围**:SetNote / RemoveNote / Note / ListNotes / Commit / Log / Diff,CLI `kb note set|get|rm|ls`、`kb log`、`kb diff`。

**验收标准**
- 写入 → 读取 roundtrip:标题、标签、正文、链接逐字段一致
- 连续修改两篇笔记后,`kb log` 显示两个快照,parents 链正确
- diff 正确输出 added / removed / updated 三类变更
- **链接跨版本自洽**:修改笔记 A 后,旧快照中指向 A 的链接仍解析到旧 A;新快照解析到新 A
- 删除 slug 后,新快照的 tree 不含该条目,旧快照不受影响

## M3 同步与运维

**范围**:Pull(祖先检查 / --force)、GC、FSCK 及 CLI。

**验收标准**
- 两个独立库(同实例双库即可)之间 pull,只传输本地缺失的对象(传输计数可验证)
- 本地头是远端头祖先 → fast-forward 成功;已分叉 → 拒绝并提示,`--force` 才覆盖
- GC 后 fsck 通过;GC 不删除任何分支可达对象
- fsck 对被篡改字节报错且退出码非零;对缺失引用(note.body 指向不存在的 blob)报错

## M3.5 项目隔离(schema v2)

**范围**:projects 表 + branches 复合主键((project, name))、库 schema 版本与对象编码版本解耦、旧库拒绝并指引重建(不做自动迁移)、repo/CLI 项目作用域(KB_PROJECT / -p)、短标识项目内解析。

**验收标准**
- v1 存量库打开时拒绝服务并给出清库重建指引,不做自动迁移(老数据可弃;由测试覆盖)
- 未指定项目时行为与 v1 一致(default);不同项目间 note/ls/log 互不可见
- 项目内短标识解析不命中他项目快照(限定当前项目可达集)
- 同库跨项目 pull 零对象传输(仅分支指针推进)
- GC 保持全库语义:清扫后所有项目可达对象保留,fsck 通过;分支表备份含 project 字段
- 文档三处同步:schema.sql v2、DESIGN §2/§4/§5.1/§6.4/§8.2、本节

**状态**:已交付(旧库拒绝与指引、项目作用域、项目内解析、跨项目 pull 零传输及全部验收用例落地)。

## M3.6 回退与历史读取

**范围**:repo Reset(祖先校验/放弃计数)、CLI `kb reset`、`note get --at` 历史读取。

**验收标准**
- 回退后分支头指向目标快照,log/ls 不再显示被放弃提交,放弃计数正确
- 回退目标非当前头祖先时拒绝;回退到当前头为空操作;空分支拒绝回退
- note get --at 按指定快照读取旧版本内容;不存在的 slug 报错
- CLI 级验证:reset 与 --at 端到端流程

**状态**:已交付。

## M3.7 AI 选用元数据(schema v3)

**范围**:projects/branches 增加 description 列、库 schema 版本 3(仅加列;旧库拒绝并指引重建,不做自动迁移)、store 描述读写契约(建项目带描述/描述就地更新/分支推进不覆盖描述)、CLI 发现出口(`kb project create --desc`、`project desc`、`project ls --json`、`kb branch ls [--json]`、`branch desc`)、`kb note ls --json` 展示层派生摘要(不改对象格式与地址)。

**验收标准**
- 全新库 init 后 schema_version=3;v2 存量库打开时拒绝服务并给出重建指引(由测试覆盖)
- 建项目可带描述,描述可就地更新;`kb project ls --json` 含 name/description/branches
- `kb branch ls --json` 含 description;分支推进(提交/reset)不清空既有描述
- `kb note ls --json` 含派生摘要(标题+正文首段截断);对象编码与地址不变(fsck 通过)
- 文档三处同步:schema.sql v3、DESIGN §2/§4.1/§4.6/§5.1、本节

**状态**:已交付(描述列与版本门禁、store 读写契约、CLI 发现出口、派生摘要、e2e 全绿)。

## M3.8 目录层级(schema v4)

**范围**:tree 条目带类型(note|dir,dir 指向子 tree,目录可嵌套)、库 schema 版本 4(仅对象编码演进,表结构不变;旧库拒绝并指引重建,不做自动迁移)、条目全路径模型(目录链 + slug,`/` 分隔;单段路径与 M2 扁平用法兼容)、repo 路径读写与目录操作(Mkdir mkdir-p 幂等 / RemoveDir 空目录限制 + --force 递归 / DirLs / DirTree)、CLI `kb dir add|ls|rm|tree` 与 note 命令路径化、diff 路径级递归比较、fsck 条目类型一致性校验。

**验收标准**
- 全新库 init 后 schema_version=4;v3 存量库打开时拒绝服务并给出重建指引(由测试覆盖);v3 旧格式 tree(无 type)在 v4 解码时响亮拒绝
- 嵌套路径条目 set/get roundtrip 逐字段一致;各层级 `note ls` 递归可见;`note get --at` 历史读取随路径自洽
- 目录可多层嵌套;`dir add` 幂等(重复建不产生新快照);空目录是合法实体且 GC 不回收;删除条目后其所在目录保留
- 中间段是条目、目标是目录、非法路径(`a//b`、`.`、`..` 等)均响亮失败
- `dir rm` 非空目录拒绝,`--force` 递归删除;旧快照时间旅行不受影响;reset 放弃历史后 GC 清扫被删子树且 fsck 通过
- diff 按全路径输出;目录间移动表现为旧路径 removed + 新路径 added 且地址不变(内容寻址)
- 输出契约变化已记录(DESIGN §4.6):`note ls --json` 新增 path、`note get` 输出 path、diff 键为全路径、新增 `dir ls --json`
- 文档三处同步:schema.sql v4、DESIGN §2/§3/§4.1/§4.6/§4.7/§5.1/§6.1/§6.4/§6.5、本节

**状态**:已交付(嵌套路径模型、目录操作与 CLI、diff/GC/FSCK 适配、schema v4 门禁、单元 + 集成测试全绿)。

## M3.9 库级运维命令(backup / restore / wipe)

**范围**:`kb backup [文件]` 整库导出(.ckb,JSONL 流式:header + 对象 base64 + 项目 + 分支;全库语义,不受项目作用域影响)、`kb restore <文件> [--force]` 导入时逐对象重算哈希校验、非空库拒绝(force 先 Wipe)、`kb wipe [--force]` 清空整库重置为全新初始化库(TRUNCATE 四表 + 重新播种 schema.sql)、store 契约新增 Wipe。

**验收标准**
- 备份/恢复 roundtrip:对象/项目/分支计数一致;嵌套条目、项目与分支描述逐字段还原;恢复后 fsck 通过
- 损坏备份(对象字节被篡改)导入时被哈希校验响亮拒绝;文件头 schema_version 不符时拒绝并提示配套版本
- 非空库恢复默认拒绝并说明,`--force` 先清空后恢复(覆盖语义可验证)
- `kb wipe` 无 `--force` 只预览不执行;`--force` 后对象/分支清零、仅剩 default 项目、库可继续写入、fsck 通过
- e2e 覆盖原生命令断言;文档同步:DESIGN §5.1/§8.3、本节

**状态**:已交付(三条命令 + store.Wipe 契约 + 单元/集成/CLI/e2e 测试全绿)。

## M3.10 存储后端可插拔(SQLite 默认,PostgreSQL 可选)

**范围**:store.SQLite 实现(全 Store 契约含 Wipe)+ schema_sqlite.sql 语义镜像(schema v4 与 schema.sql 同步演进)、store.Open 按 DSN 分派(`postgres://` → PG,其余按 SQLite 路径,默认 `~/.local/share/caskb/caskb.db`)、CLI 全量面向 store.Store 接口、`kb init` 显示后端、repo/store 集成测试默认 SQLite 零外部依赖(KB_TEST_DSN 时回归 PostgreSQL)、e2e 双模式(SQLite 默认 / postgres 可选,含 pg_dump 备份路径)。

**验收标准**
- 全新环境零外部依赖:kb init/note/dir/log/diff/reset/project/branch/gc/fsck/backup/restore/wipe 全流程可用(默认 SQLite 文件;`kb init` 显示后端 sqlite)
- `KB_DSN=postgres://…` 时行为与 M3.9 交付一致;两后端跑同一套 go test 用例与同一份 e2e 脚本全绿
- schema_sqlite.sql 与 schema.sql 语义一致:整库版本门禁同样拒绝旧版本并指引重建;外键兜底(分支→对象、分支→项目)同样响亮报错
- .ckb 备份跨后端可移植:SQLite 导出 → PG 恢复(及反向)roundtrip 计数与内容一致,fsck 通过
- 文档同步:schema_sqlite.sql、DESIGN §4/§5/§8、AGENTS、README、本节

**状态**:已交付(SQLite 后端与 DSN 分派、双后端测试/门禁/e2e 全绿,跨后端 .ckb 可移植)。

## M3.11 dir tree 全库视图(增量)

**范围**:纯展示层增量,无数据模型与对象格式变更——`kb dir tree` 未显式指定项目(`-p`/`KB_PROJECT` 均未设置)且未给路径时,渲染全库视图:`(root)/` 下项目为顶层节点,逐项目挂其默认分支树;显式指定项目保持单项目树不变。

**验收标准**
- 多项目库中不带 -p 执行 `kb dir tree`:每个项目都出现为顶层节点,笔记与目录挂各自项目之下,不串项目
- 显式 `-p 项目` 时输出与 M3.8 以来单项目树行为一致
- 无分支的空项目显示 (空);`KB_BRANCH` 对全库视图中各项目统一生效
- 文档同步:DESIGN §4.6、usage 帮助文本、本节

**状态**:已交付(CLI + 单元测试)。

## M3.12 Markdown 互操作(增量)

**范围**:纯互操作层增量,无数据模型与对象格式变更——CLI `kb export md <目录> [--at 快照] [--force]`(当前分支或历史快照的全部条目导出为镜像 .md 文件树:条目路径去 .md 为文件、目录为子目录;目标文件已存在时整批拒绝并提示 --force,绝不部分写入)与 `kb import md <目录> [-m msg]`(递归扫描 .md:相对路径去 .md 为条目路径;非 .md 文件、非法路径(`a//b`、`.`、`..` 等)、中间段是条目、front-matter 缺 title 均响亮列出问题文件并整批拒绝;title 必填、tags 可选逗号分隔,正文为 front-matter 之后原文字节;全部解析成功后走 BulkImport 等价路径:一次提交 + 一次索引增量);repo 层 Markdown 编解码(EncodeMdNote/DecodeMdNote)与导入导出契约(ExportMarkdown/ImportMarkdown,ListNotesAt 历史快照读取);roundtrip:export(import(X)) 与 X 逐字节一致、import(export(库)) 写回后 diff 零变更(地址不变)。两命令受 -p/KB_PROJECT 项目作用域约束。

**验收标准**
- 编解码往返逐字节一致(标题/标签/正文;无标签省略 tags 行、空正文、末行无换行等边界)——`TestMarkdownEncodeDecodeRoundtrip`;front-matter 违规响亮报错——`TestMarkdownDecodeErrors`
- import(export(库)) 写回后 diff 零变更、条目地址不变:未改动库重导不产生新快照;改动/删除后重导逐字节还原(时间源递增下仍成立)——`TestMarkdownRepoRoundtrip`;空导入报错、空库导出 0 条——`TestMarkdownImportEmpty`
- CLI 集成:export → 改库 → import → diff 断言零变更 → 再次 export 与首次逐字节一致(--at 历史快照导出同样一致;内容未变再导入无新快照)——`TestMarkdownCLIExportImportRoundtrip`;已存在文件整批拒绝并提示 --force、--force 覆盖——`TestMarkdownCLIExportForce`;问题文件(缺 title、非 .md、中间段是条目)响亮列出且整批不写入——`TestMarkdownCLIImportProblems`;非法相对路径(`a//b.md`、`./a.md`、`.md` 等)拒绝——`TestMarkdownEntryPathValidation`
- e2e markdown 段:导入 → 检索命中 → 导出 → roundtrip 断言(专用项目内 `diff -r` 逐字节一致、重复导出拒绝、改库后重导还原、再导入零变更、问题文件整批拒绝)——`scripts/e2e.sh`

**状态**:已交付(repo 单元 + CLI 集成 + e2e 全绿;`go test ./internal/repo/ ./cmd/kb/ -run Markdown -v`)。

## M4 检索与集成

**范围**:倒排索引分片纳入快照、搜索命令或 HTTP API。

**交付(M4 CLI 批次,schema v5)**:倒排索引两类 CAS 对象(indexroot/indexshard)纳入快照(index 字段);CJK 2-gram 确定性分词;BM25 检索 `kb search`(字段加权:标题3/标签2/正文1;确定性排序:分数降序→路径升序→地址;--at 历史快照检索);commit 内增量分片重建(结构共享)+ `kb index rebuild` 全量重建;顺带交付链接 slug 解析 `kb link resolve`(§3.3 规则落地)与 `store.ProjectGet` 契约补全。

**验收标准(全部已验)**
- 同一快照重复搜索,结果与顺序完全一致(可复现)——e2e diff 断言 + 单元测试
- 更新一篇笔记后,只有受影响分片地址变化,其余分片结构共享——TestM4_IndexStructuralSharing
- (若做 HTTP API)对同一快照的读写经 API 与 CLI 结果一致——**已落地**:`kb serve` 只读 HTTP API(DESIGN §8.5;TestServeCLIParity 对 search/projects/diff 逐字段钉死 API 与 CLI --json 相等,顺序亦相等;e2e serve 段后台起服务 + curl 断言 healthz/note/search/POST 405 + 优雅退出)

**验收命令**
- `go test ./internal/index/ ./internal/repo/ -run "TestM4|TestSearch"`
- `go test ./internal/server/ ./cmd/kb/ -run TestServe -v`
- `./scripts/e2e.sh`(含 M4 段:命中/确定性/--json/--at/rebuild/fsck;含 serve 段:后台起服务、curl 断言 healthz/note/search/POST 405、kill 优雅退出)

## M4.1 写入型 HTTP API(增量)

**范围**:在 §8.5 只读 API 之上新增恰好两个写端点——`POST /api/v1/note`(等价 `kb note set`,成功 201 + {path,address,short})与 `DELETE /api/v1/note?path=`(等价 `kb note rm`,成功 200 + {removed,short}),直接复用 repo.SetNote/RemoveNote,不暴露 stage/bulk/其他写命令;令牌鉴权 `--token <值>` / `KB_SERVE_TOKEN`(旗标优先,内存常量时间比较,不写日志不回显),**未配置令牌时服务保持纯只读(写端点一律 403)**;锁忙(serve 与 CLI 并发写)返回 503 +「稍后重试或改用 CLI」,不产生半写状态,写后 fsck 可过、检索立即可见;`kb note get` 补 `--json`(与 GET /api/v1/note 同构,internal/view 契约);文档四处同步(DESIGN §8.6 / 本节 / README / CHANGELOG)。

**验收标准**(与测试一一对应)
- 鉴权矩阵:未配置令牌 POST/DELETE 403(「服务未配置写入令牌…」文案);配置后缺头 401 / 错令牌 401 / 对令牌 POST 201;响应不回显令牌;读端点保持无鉴权——`TestServeWriteAuthMatrix`(internal/server)
- 写入后读回一致(POST 201 + {path,address,short};GET 读回逐字段相等)且 POST 后 fsck 零问题——`TestServeWriteReadback`
- 写入后立即可 search(索引增量同步完成后再返回响应)——`TestServeWriteSearchImmediate`
- 非法路径 400:缺 path/缺 title/空段(a//b)/保留段(../x)/中间段是条目/目标是目录,沿用 CLI 可行动文案;DELETE 缺 path 400——`TestServeWriteBadPath`
- 删除语义:DELETE 200 + {removed:1,short};删除后 GET 404;重复删除 404;DELETE 后 fsck 可过——`TestServeWriteDelete`
- 并发写锁忙 503 + 可行动提示(锁占用模拟)——`TestServeWriteLockBusy503`
- CLI/API parity:API POST→CLI `note get --json` 读回逐字段相等;CLI note set→API 读回相等;API DELETE→CLI note get 404 语义——`TestServeWriteCLIParity`(cmd/kb)
- e2e 写 API 段:带 KB_SERVE_TOKEN 起 serve → curl POST(缺/错令牌 401、对令牌 201)→ curl search 立即命中 → CLI note get 断言 → curl DELETE → CLI 确认删除 → 重复删除 404;再起无令牌实例断言 POST/DELETE 403——`scripts/e2e.sh`

**验收命令**
- `go test ./internal/server/ -run TestServeWrite -v`
- `go test ./cmd/kb/ -run TestServeWrite -v`
- `./scripts/e2e.sh`

**状态**:已交付(令牌鉴权 + 两个写端点 + 503 语义 + note get --json;internal/server 六个 TestServeWrite* + cmd/kb TestServeWriteCLIParity + e2e 写 API 段全绿)。

## M4.2 检索片段高亮(增量)

**范围**:BM25 检索命中附带「命中片段」——纯展示层增量,**评分/排序/命中集合零变化**。`kb search` 增布尔旗标 `--snippet`(文本模式命中行下追加 4 空格缩进片段行,命中词元以【】包裹;`--json` 增可选字段 `snippet`,omitempty,缺省不带,旧消费者零破坏);`GET /api/v1/search` 增可选查询参数 `snippet=1`(仅字面 1 生效,语义与 CLI 相同,JSON 同字段);确定性窗口算法:任一查询词元首次出现为中心、目标 80 rune、截断边缘向内回望 20 rune 吸附标点/空白、rune 对齐不劈多字节字符;CJK 2-gram 标记扩展回完整词源(「知识库」→【知识库】),英文按词边界(chan 不命中 channel),孤字词元仅命中段长 1 的孤字——与索引分词同口径;无任何词元命中 body(仅标题命中)时取开头窗口、无标记(二选一钉死);文档四处同步(DESIGN §7.1 / 本节 / README / CHANGELOG)。

**验收标准**(与测试一一对应)
- 算法单测(名字含 Snippet):中英混合、英文词边界、CJK 词源扩展、标题命中钉死、窗口边缘吸附、rune 边界(不劈多字节)、分词同口径、确定性逐字节一致——internal/view TestSnippet*(含 TestSnippetNoBodyHit)
- CLI:文本片段行与【】标记、--json snippet 字段存在/缺省双向断言、确定性——cmd/kb TestSearchSnippetCLI
- 排序不变红线:同一查询带/不带 --snippet 的结果序列(路径+分数)完全一致——TestSearchSnippetCLI 与 TestServeSearchSnippet 双侧断言
- API:snippet=1 附片段且标记词元、缺省无字段、snippet=0/true 视为缺省、响应逐字节确定——internal/server TestServeSearchSnippet
- CLI/API parity 含 snippet 字段——TestServeCLIParity 扩展(assertSearchParity 逐字段比较 snippet)
- e2e snippet 段:文本/缩进/排序不变/--json 字段/缺省无字段/serve snippet=1——scripts/e2e.sh

**验收命令**
- `go test ./internal/view/ -run TestSnippet -v`
- `go test ./cmd/kb/ -run TestSearchSnippet -v`
- `go test ./internal/server/ -run TestServeSearchSnippet -v`
- `go test ./cmd/kb/ -run TestServeCLIParity -v`
- `./scripts/e2e.sh`

**状态**:已交付(internal/view 七个 TestSnippet* + cmd/kb TestSearchSnippetCLI + internal/server TestServeSearchSnippet + TestServeCLIParity 扩展 + e2e M4.2 段全绿)。

## M5 三方合并(pull --merge)

**范围**(两批次,批次 A 为批次 B 前置;调研 docs/research/merge-design.md §2/§4/§5):批次 A(repo 内核,已交付)——LCA 基准计算(沿 parents 链 BFS,不信任 Time;无共同祖先拒绝;多候选拒绝 + 显式指定)、条目级三方树合并纯函数(判定表全类别、Merkle 剪枝、目录递归合成)、冲突结构 {path, kind, base, ours, theirs}、零冲突落库(合并快照 Parents=[ours, theirs] + 索引增量)。批次 B(CLI/中间态/收束,本批)——`kb pull --merge`(与 `--force` 互斥;判定矩阵含「本地领先 → 已更新」修正)、`<branch>-merge` 中间态分支(基线快照 = 自动合并树,冲突条目 ours 占位)+ meta 键(单键 JSON:base/theirs/ours + 冲突清单)、冲突清单输出(文本逐行 路径/类别/三侧短标识,退出码非零)、`kb merge --continue [-m]` / `kb merge --abort` 收束、冻结纪律(合并中态拒绝 note/dir/bulk/reset/pull/index rebuild/普通 commit;--stage 升格为裁决写入 -merge 视图;kb stage 展示裁决清单)、`kb log` 合并行追加第二亲短标识、usage 与文档四处同步。

**验收标准**(与测试一一对应)

- 判定表逐类、LCA、Merkle 剪枝、双亲快照兼容性(fsck/GC/backup/pull/reset)、本地领先空操作、冲突清单完整性——批次 A `internal/repo` TestMerge*(merge_test.go)
- 中间态建立与读回(meta 键 + main-merge 基线快照形态)、冻结守卫覆盖 note set/rm、dir add/rm、bulk、reset、index rebuild、commit、pull(含 --force)、--stage 裁决写入 -merge 视图(不触碰 -stage)、--continue 双亲合并快照 + 索引 + 中间态清理 + 检索命中、零裁决拒绝、--abort 放弃裁决数与指针不动、无共同历史不建态、已有中间态再发起拒绝、删改对撞裁决闭环——`internal/repo` TestMergeState*(mergestate_test.go)
- pull --merge 零冲突一步完成(冲突 0 条 + 合并快照输出 + fsck + 双侧检索)、冲突中间态(清单字段/退出码非零/指针不动)、--abort(中间态清理 + 回到合并前 + 无中间态指引)、--continue(裁决稿可见 + 双亲 log 行 + 检索 + fsck + 分支清理)、--force/--merge 互斥、log 双亲展示(两库头短标识无序对)、冻结提示(直接写/pull/commit 拒绝且读不受限)——`cmd/kb` TestMergeCLI*(merge_cli_test.go)
- e2e 全流程(两库互 pull):共同基点 → 零冲突双亲落库断言 → 冲突(清单 + 退出码 + 指针不动)→ 无旗标 pull 拒绝文案含 --merge 指引 → 冻结 → --abort → 再走 --continue 裁决闭环(检索命中 + fsck)→ 互斥——`scripts/e2e.sh` 合并段
- store 契约:MetaDelete(SQLite/PG 双后端,幂等)——`internal/store` TestMeta*
- 冷启动 D1(T44):远端项目存在但分支不存在(零提交)pull → 「已是最新」空操作——本地有/双空两形态、--force 与 --merge 同为空操作、远端项目不存在仍响亮报错——`internal/repo` TestMergeColdPullRemoteEmptyNoop(mergecold_test.go)+ `cmd/kb` TestMergeColdCLIPullRemoteEmptyNoop + e2e coldstart 段
- 冷启动 D2(T44):无共同历史缺旗标拒绝且新文案分流(真分叉仍指 --merge,无共同历史指 --force 或 --merge --allow-unrelated);旗标单独给或与 --force 同给响亮拒绝;空基线三方合并零冲突落双亲(两侧新增全取、双侧检索、fsck、对侧 ff、幂等 no-op),同路径异地址 content 冲突进既有中间态并以 --stage/--continue 闭环——`internal/repo` TestMergeColdUnrelated*(mergecold_test.go)+ `cmd/kb` TestMergeColdCLIUnrelated*(mergecold_cli_test.go)+ e2e coldstart 段

**验收命令**

- `go test ./internal/repo/ -run "Merge" -v`
- `go test ./cmd/kb/ -run "MergeCLI" -v`
- `go test ./cmd/kb/ -run "MergeCold" -v`(T44 冷启动 CLI;internal 侧由 `-run "Merge"` 覆盖 TestMergeCold*)
- `go test ./internal/store/ -run TestMeta -v`
- `./scripts/e2e.sh`(新增 merge 段)

**状态**:已交付(A 批次:internal/repo/merge.go + TestMerge*;B 批次:internal/repo/mergestate.go + TestMergeState*、cmd/kb pull --merge / merge --continue|--abort / stage 合并态 / log 双亲 + TestMergeCLI*、scripts/e2e.sh 合并段全绿;T44 冷启动批次:internal/repo TestMergeCold* + cmd/kb TestMergeColdCLI* + e2e coldstart 段全绿——空远端空操作 + --merge --allow-unrelated 空基线合并 + 分叉文案分流)。

## M6-A 向量对象模型与嵌入重建(T54,已交付)

**范围**(DESIGN §7.3/§4.9;四条红线:不内嵌模型运行时 / 不引入向量数据库 / 向量按 model_id 版本化入内容 / 不做检索集成——默认检索仍是 BM25,检索面属 M6-B):对象模型两类新 kind(schema v6)——`vecshard` 规范 JSON `{kind, model, dim, items:[{path, vec}]}`,字段定序、items 按路径排序、float32 向量按 little-endian 拼接后 base64(跨平台逐字节确定);分桶 FNV-1a(全路径)%64 与 indexshard 同构;`vecroot` 照 indexroot 范本列各桶地址(model/dim 入内容故跨模型必不同址);snapshot 加可选 `vec`(omitempty,无向量快照编码逐字节不变);两份 DDL kind 约束放宽 + meta 播种 6 + v5 库拒开门禁 + v6 解码拒绝不匹配编码。Embedder 适配器(internal/embed)——`Embedder{Model/Dim/Embed}` 接口 + Ollama `/api/embed` 适配器(字段名与批量顺序语义经官方文档 docs/api.md@b79067b 核实写入注释;HTTP 超时 30s;KB_EMBED_MODEL 未设置=功能整体关闭给可行动报错,不静默跳过)。重建与配套——`kb index rebuild --embed` 全量重建(标题+正文逐条嵌入、按桶聚合写对象、快照带 vec 落库、BM25 地址沿用;嵌入失败响亮中止可重试);fsck 向量一致性(分片 model/dim 与根一致、items 路径存在于对应快照,无 vec 快照跳过不报);GC 与 indexshard 同规则可达回收(gc.keep_last 同精简);vecshard/vecroot 走 store gzip 压缩同待遇。

**验收标准**(与测试一一对应,名字含 Vector)

- 编码确定性:同输入两次编码逐字节同址;float32 小端 base64 与手工字节一致;字段定序与 items 排序;快照 vec omitempty——`internal/object` TestVectorVecShardCanonicalEncoding / TestVectorVecBase64LittleEndian / TestVectorVecRootCanonicalEncoding / TestVectorSnapshotVecOmitEmpty
- 跨模型不同址:model/dim 内嵌内容,换模型/换维度必换地址——`internal/object` TestVectorCrossModelDifferentAddr + `internal/repo` TestVectorRebuildCrossModelDifferentAddr
- rebuild roundtrip(假 Embedder 固定向量,零网络):逐条嵌入输入=标题+空行+正文、分桶正确、向量逐位还原、幂等同址、嵌入失败响亮中止分支不动、与 BM25 索引共存沿用——`internal/repo` TestVectorRebuildRoundtrip / TestVectorRebuildEmbedFailureAbortsLoudly / TestVectorRebuildCoexistsWithBM25
- fsck 一致性:正常库零问题;items 路径缺失=fail;分片 model 与根不一致=fail;无 vec 快照跳过——`internal/repo` TestVectorFSCKConsistency
- schema v6 门禁:v5 库拒开并指引重建——`internal/store` TestVectorSchemaV6GateRejectsV5Library
- GC 同规则:悬垂向量对象清扫、可达保留、keep_last 水位与 BM25 一视同仁——`internal/repo` TestVectorGCSweepsAndKeeps
- gzip 兼容:大 vecshard 落库带 0x01 前缀、Get 逻辑字节一致、往返无损——`internal/store` TestVectorShardGzipCompressionCompat
- Embedder 超时与错误文案:超时/HTTP 非 200/连接失败/数量不符全部含服务地址+模型+下一步动作;未配置给可行动报错——`internal/embed` TestVectorEmbed*(六项)
- CLI 端到端(本地 httptest 假 Ollama,零外网):rebuild --embed 输出 vec/snapshot、fsck 通过、BM25 检索不变、未配置报错——`cmd/kb` TestVectorIndexRebuildEmbedCLI

**验收命令**

- `go test ./internal/repo/ ./internal/object/ ./internal/embed/ -run Vector -v`
- `go test ./internal/store/ ./cmd/kb/ -run Vector -v`
- `./scripts/verify.sh`

**状态**:已交付(internal/object vector.go + EncodeVecBase64、internal/embed embed.go、internal/repo vecbuild.go/fsck/gc/childrenOf、cmd/kb index.go --embed、schema v6 两份 DDL;上述 Vector 测试全绿,verify 全绿)。M6-B(检索面)见下节,已交付。

## M6-B 混合检索集成(T55,已交付)

**范围**(DESIGN §7.3;四条红线:不内嵌模型运行时 / 不引入向量数据库 / 向量按 model_id 版本化入内容 / **BM25 默认不动**——hybrid 是显式可选旗标,缺省调用零变化):`kb search --hybrid` 与 `GET /api/v1/search?mode=hybrid`(语义与 CLI 完全一致,TestServeCLIParityHybrid 钉死)——BM25 词法腿与向量余弦语义腿各取前 50 名做 **RRF 融合**(score = Σ 1/(60+rank),k=60 固定常数不设旋钮,rank 从 1 起两腿独立),输出融合分降序、平局路径升序;查询词经 Embedder 恰好 1 次嵌入(30s 上限);向量腿对快照 vec 分桶平扫(量级沿用 §7 定论:≤十万条精确遍历优于 ANN)。前置红线(一律响亮报错绝不静默降级):快照无 vec / 模型不一致(含维度不符)→ 指引 `kb index rebuild --embed`;KB_EMBED_MODEL 未设置 → 可行动配置报错(设置方法四步);嵌入失败原样上抛。`--json` 行内增可选字段 `mode:"hybrid"`(omitempty 仅 --hybrid 时存在,score 为融合分,与 --snippet 可叠加)。serve 进程同读 KB_EMBED_*(未配置不拦启动,mode=hybrid 按请求 409);hybrid 失败语义 409(未配置/无向量/模型不一致/嵌入失败)、mode 非法取值 400,均与 CLI 同文案。

**验收标准**(与测试一一对应,名字含 Hybrid)

- 前置与失败语义:无 vec / 模型不一致 / 嵌入失败三哨兵错误与可行动文案(rebuild --embed 指引、KB_EMBED_MODEL 设置方法)、nil Embedder、失败不降级——`internal/repo` TestHybridSearchNoVec / TestHybridSearchModelMismatch / TestHybridEmbedFailureNoDegrade / TestHybridNilEmbedder
- 查询嵌入红线:恰好 1 次调用——`internal/repo` TestHybridEmbedOnce
- RRF 算术与平局:两腿排名互换 → 融合分相等(1/61+1/62)→ 路径升序;零向量约定余弦 0——`internal/repo` TestHybridRRFTieBreak
- 融合深度:两路各取前 50(120 条语料输出 ≤100)、分数降序——`internal/repo` TestHybridTopKDepth
- 空库/--at 语义:空库无结果;历史快照无 vec 报错与词法 --at 同构——`internal/repo` TestHybridEmptyRepo / TestHybridAtHistoricalSnapshot
- 评测集(证明有效不是感觉):tests/eval 固定语料 23 条中文条目(同义不同词/上下位/中英混写/纯代码 ID)+ 15 条查询(12 语义 + 3 词法),假 Embedder 主题轴固定表;语义查询 hybrid recall@5 逐条严格优于纯 BM25(12/12=1.0 vs ≤0.25)、代码/ID 查询两模式都命中、确定性逐字段——`internal/repo` TestHybridEval
- CLI 端到端(本地 httptest 假 Ollama,零外网):报错路径(无 vec/未配置)、mode 字段与向后兼容、--snippet 叠加、确定性、BM25 不受影响——`cmd/kb` TestSearchHybridCLI
- CLI/API parity:mode=hybrid 逐字段(含 mode 与融合 score)与顺序相等、缺省无 mode 字段——`cmd/kb` TestServeCLIParityHybrid
- API 错误矩阵:成功路径(mode 字段/融合分区间/逐字节确定)、未配置 409、无向量 409、模型不一致 409、嵌入失败 409、非法 mode 400、缺省契约不变——`internal/server` TestServeSearchHybridModeOK / TestServeSearchHybridErrors / TestServeSearchHybridModelMismatch / TestServeSearchHybridEmbedFail

**验收命令**

- `go test ./internal/... ./cmd/kb/ -run Hybrid -v`
- `./scripts/verify.sh`(e2e 含 hybrid 报错路径腿)

**状态**:已交付(internal/repo hybrid.go(SearchHybrid/cosineRank/RRF 哨兵)、tests/eval corpus.go、cmd/kb search.go --hybrid、internal/server mode 参数与 409 映射、internal/view mode 字段;上述 Hybrid 测试全绿,verify 全绿)。演进项(融合权重/Top-K 旋钮/量级复核)未立项,触发条件沿用 §7.2「新 workload 证据」纪律。
