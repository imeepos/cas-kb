# Changelog

本文件记录面向用户的显著变更;格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
升级操作指引见 [docs/upgrade.md](docs/upgrade.md)。

## v0.8.0 - 2026-09-03

### Added
- 混合检索(M6-B/T55,DESIGN §7.3):`kb search --hybrid` 与 `GET /api/v1/search?mode=hybrid`(两条出口逐字段同构)——BM25 词法腿与向量余弦语义腿**各取前 50 名做 RRF 融合**(score = Σ 1/(60+rank),k=60 固定常数不设旋钮),输出融合分降序、平局路径升序;查询词经嵌入服务恰好 1 次调用(30s 上限);同义词/上下位/中英混写可召回(词法零命中仍可命中)。`--json` 行内增可选字段 `mode:"hybrid"`(`omitempty` 仅 --hybrid 时存在,score 为融合分,与 `--snippet` 可叠加);**BM25 默认不动**——缺省调用输出与分数逐字节不变。前置失败一律响亮报错绝不静默降级:快照无向量或模型不一致 → 指引 `kb index rebuild --embed`;`KB_EMBED_MODEL` 未设置 → 含设置方法的可行动报错;嵌入失败原样上抛。可复现性边界 = 同快照 + 同 model_id(同向量数据 → 结果与顺序逐字段确定)。API 失败语义:hybrid 前置/执行失败 409(与 CLI 同文案),`mode` 非法取值 400;serve 进程同读 `KB_EMBED_*`(未配置不拦启动,启动横幅注明)
- 语义检索评测集(tests/eval):23 条中文知识条目固定语料(同义不同词/上下位/中英混写/纯代码 ID 类)+ 15 条查询(12 语义 + 3 词法),假 Embedder 按「主题词→轴」固定表构造向量;单测钉死语义类查询 hybrid recall@5 逐条严格优于纯 BM25(12/12 全 1.0 vs 最高 0.25)、代码/ID 类查询两模式都命中(词法无损)
- 向量对象模型与嵌入重建(M6-A/T54,DESIGN §7.3,**schema v6**):新增 `vecshard`/`vecroot` 两类内容寻址对象与快照可选 `vec` 字段——语义向量随快照冻结入库,float32 按 little-endian 拼接后 base64 保证跨平台逐字节确定,`model`/`dim` 写进内容故**跨模型必不同址**;嵌入走外挂服务(不内嵌运行时、不引入向量数据库),`kb index rebuild --embed` 全量重建(逐条笔记标题+正文嵌入、FNV-1a(路径) 64 桶分片、快照带 vec 落库、BM25 索引地址沿用;嵌入失败响亮中止可重试);fsck 增向量一致性校验(分片 model/dim 与根一致、items 路径存在于对应快照,无 vec 快照跳过);GC 与 `gc.keep_last` 对向量对象与倒排索引同规则;vecshard/vecroot 走透明 gzip 压缩(SQLite 后端)
- Embedder 适配器(internal/embed):`KB_EMBED_MODEL`(嵌入模型名,未设置=向量功能整体关闭并给可行动报错,不静默跳过)与 `KB_EMBED_URL`(Ollama 兼容 `/api/embed`,默认 `http://127.0.0.1:11434`)两个新配置项;HTTP 超时 30s,错误文案含服务地址/模型与下一步动作;Ollama 字段名与批量顺序语义经官方文档核实并注释于代码

### Changed
- **库 schema 版本升至 6**(仅放宽 `objects.kind` 约束 + meta 播种值;表结构与 v5 一致):v5 存量库在新版本下**拒绝打开**并指引清库重建(不做自动迁移);老数据可弃则清库重建后 `kb init`,或留在旧版本二进制上继续使用
- 混合检索为可选旗标,缺省检索仍是 BM25:`kb search`(不带 `--hybrid`)与 `/api/v1/search`(不带 `mode`)行为零变化(原 M6-A「本批不含语义检索面」说明随 M6-B 交付作废)

## v0.7.0 - 2026-09-03

### Added
- 健康自检 `kb doctor`(T49,DESIGN §8.7):`kb doctor [--json] [--check <name>…] [-l|--list-checks] [-p 项目]` 一站拉通库完整性/版本/配置/serve 可达性;六检查项(storage/fsck/version/config/gc-protect/serve)全部复用现成能力(fsck/version//healthz/config 核对),逐项输出「ok/warn/fail + 人话 + 可行动修复建议」,汇总行 `doctor: N ok, M warn, K fail`;**有 fail 退出码 1,仅 warn 不拦**(brew doctor 式克制;悬垂/未达对象按 git fsck --dangling 分级为 warn,serve 未运行是 ok 不是错);`--json` 走 internal/view.DoctorRow 契约;输出绝不回显连接串凭据段与令牌值
- 合并状态查询端点 `GET /api/v1/merge-state`(T48,DESIGN §8.5):`?project=&branch=` 省略时取 serve 进程作用域(`KB_PROJECT`/`-p` 与默认分支);响应含 `project/branch/state/can_continue/can_abort/base/theirs/ours/conflicts/conflict_count/merged_branch`,`state ∈ idle|merging`——无合并中态返回 200 + `state:"idle"`(轮询稳态:事实字段 null、conflicts 空数组、两布尔 false),合并态事实字段取合并中间态 meta 键,conflicts 与 CLI 冲突清单同一份契约(internal/view);项目或分支不存在 404,参数空白 400,非 GET 405
- `kb stage --json`:输出与 `GET /api/v1/merge-state` 同构的合并状态行(internal/view 一份契约两条出口,TestServeMergeStateParity 逐字段钉死);文本模式行为不变;merge-design §4-9「最小只读暴露」开放问题闭合
- 冷启动完成提示行(T47-D,调研 §2.3):`pull --merge --allow-unrelated` 空基线合并成功后追加输出「冷启动完成:两侧历史已建立共同祖先,后续 pull 无需 --allow-unrelated」(仅冷启动合并出现,普通三方合并不受影响);README「多机同步」段增冷启动三步指引(自证→对拉→收敛),docs/serve.md 多机部署处交叉引用
- 演练固化脚本(T47-C,范式蓝本 docs/research/best-practices-adoption.md §3.3,git t/ 四件套):`scripts/drill-multi.sh`(T42 多端互写剧本:冷启动合并/真实冲突裁决/冻结拒绝/--abort 回滚/backup→wipe→restore→fsck 往返)与 `scripts/drill-serve.sh`(T43 serve 运维剧本:默认绑定与只读基线/令牌写入闭环/鉴权矩阵/锁忙 503)——TAP 式逐腿断言(`ok N - <腿标题>`/`not ok N - <腿标题>(原因)`,末尾 `PASS x / FAIL y (skipped z)` 汇总,FAIL>0 退出 1);`mktemp -d` + trap 清理,`DRILL_KEEP=1` 保留现场,`DRILL_RUN=<编号>` 跑子集,`KB_BIN` 指定被测二进制(缺省现场 go build);平台依赖缺失按腿 skip(lsof/openssl/sqlite3),跳过不进分母但汇总行显示;serve 进程清理挂 trap(记 PID 档案 TERM→KILL 兜底),端口 `DRILL_PORT` 默认 127.0.0.1:18787;默认不进 verify 门禁,`DRILL=1 ./scripts/verify.sh` 选择性追加

### Changed
- `pull` 无共同历史报错文案(T47-D):两条出路各配一句代价说明——`--force 覆盖:丢弃本地独有提交` vs `--merge --allow-unrelated 做空基线合并:两侧新增互不冲突即全取`,把取舍摆到报错现场(判定行为不变,仅文案)
- DESIGN §7 增「7.2 段化观测清单(草案)」(T47-E):六指标表(单写 P99/库体积及索引占比/单次写重写字节/索引对象数增速/bulk 比值/检索 P95)+ 口径/采集点/触发线;指标 1/3/5 为决策指标、2/4/6 为护栏指标,采集脚本不入 verify 门禁;声明无新 workload 证据不重启三难讨论

## v0.6.1 - 2026-09-02

### Added
- 冷启动空基线三方合并(T44,DESIGN §6.3):`kb pull --merge --allow-unrelated`——两库无共同历史(各自 init 的冷启动)时以空树为基准做三方合并,两侧条目均视为新增:同路径同地址自动合、同路径异地址按既有判定表记冲突(冲突走既有 `<branch>-merge` 中间态与 `--continue`/`--abort` 收束);旗标仅可与 `--merge` 连用,单独给或与 `--force` 同给响亮拒绝;有共同祖先时该旗标不改变任何行为

### Fixed
- 多端冷启动(T42 演练报告 D1):远端项目存在但分支不存在(远端零提交)时 `kb pull` 不再硬报错「store: 分支不存在」,改为「已是最新」空操作(exit 0),与「本地空拉非空可 fast-forward」对称;本地分支也不存在(双空)同样空操作
- 多端冷启动(T42 演练报告 D2):分叉判定拆两类文案——有共同祖先的真分叉仍提示 `kb pull --merge`;无共同历史改为提示「--force 覆盖,或 --merge --allow-unrelated 做空基线合并」,修复「指引改用 --merge 而 --merge 又拒绝」的指引断裂
- docs/serve.md(T43 演练报告 D1):launchd 示例裸 `&&` 未转义导致 plist 无法加载,改为 `&amp;&amp;`(提取实测 plutil/xmllint 通过);另补 ps -Ao 平台注记、systemd 建用户步骤、grep 计数判定说明(T43 P2/P3/P4)

## v0.6.0 - 2026-09-02

### Added
- 三方合并(M5,DESIGN §6.3):`kb pull --merge`(与 `--force` 互斥)在分叉时按最近公共祖先(LCA,沿 parents 链 BFS)做条目级三方合并——单侧变取单侧、双侧同变自动合、目录递归下钻、Merkle 地址相等整子树剪枝;不做文本行级合并。零冲突直接落库:合并快照含两个 parents(两侧历史均可达,fsck/GC/pull 传输零改动兼容),输出合并快照短标识与冲突数;有冲突全有或全无——不落提交不动指针,建 `<branch>-merge` 中间态分支与 meta 键(冲突清单存档),退出码非零并逐行输出冲突清单(路径/类别/三侧短标识,类别 ∈ content/modify-delete/type)。合并中态冻结该分支一切直接写(note/dir/bulk/reset/pull/index rebuild/普通 commit、serve 写端点),`note set/rm --stage` 升格为裁决动作(写入 -merge 视图),`kb stage` 切换为展示冲突清单与裁决进度;`kb merge --continue [-m]` 把裁决差异应用到自动合并树落双亲合并快照并清理中间态(零裁决响亮拒绝),`kb merge --abort` 删中间态回到合并前;`kb log` 合并行追加第二亲短标识。(M5-A repo 内核:LCA/判定表/剪枝/零冲突落库;M5-B:CLI/中间态/收束/冻结/e2e)
- 检索片段高亮(M4.2,纯展示层增量,DESIGN §7.1):`kb search --snippet` 文本模式在每条命中行下追加缩进片段、命中词元以【】包裹;`kb search --json --snippet` 与 `GET /api/v1/search?snippet=1` 增可选字段 `"snippet"`(缺省不带,旧消费者零破坏);确定性窗口算法(任一查询词元首次出现为中心,目标 80 字符,截断边缘吸附标点/空白,按 rune 切不劈多字节字符;CJK 2-gram 标记扩展回完整词源,英文按词边界);评分/排序/命中集合零变化

### Changed
- `kb pull` 判定矩阵修正:远端头 ∈ 本地祖先链(本地领先)时由「分叉拒绝」改为「已是最新」空操作(与 git 语义对齐);无旗标分叉拒绝的提示文案追加 `kb pull --merge` 三方合并指引;`--force` 覆盖回退语义不变

## v0.5.0 - 2026-09-02

### Added
- 写入型 HTTP API `kb serve`(M4.1,DESIGN §8.6):`POST /api/v1/note`(等价 `kb note set`,成功 201 + `{"path","address","short"}`)与 `DELETE /api/v1/note?path=`(等价 `kb note rm`,成功 200 + `{"removed":1,"short"}`),语义与 CLI 逐字段一致、直接复用 repo.SetNote/RemoveNote;令牌鉴权 `--token <值>` / `KB_SERVE_TOKEN`(旗标优先,内存常量时间比较,不写日志不回显),**未配置令牌时服务保持纯只读(写端点一律 403)**,读端点保持无鉴权;参数缺失/非法路径/路径是目录 400 沿用 CLI 可行动文案,路径不存在 404;serve 与 CLI 同时写遇锁忙返回 503 +「稍后重试或改用 CLI」,写后 fsck 可过、检索立即可见
- `kb note get --json`:单条笔记机器可读输出,与 GET /api/v1/note 同构(internal/view.NoteRow 一份契约)

## v0.4.0 - 2026-09-02

### Added
- 只读 HTTP API `kb serve`(M4 收尾):默认只绑 `127.0.0.1:8787`,暴露 `/healthz` 与 `/api/v1/projects|tree|note|search|log|diff`(全部 GET,错误统一 `{"error":…}` 的 400/404/405/500);无任何写端点(POST 一律 405),写路径只有 CLI;JSON 行契约抽到 `internal/view` 与 CLI `--json` 同构(`TestServeCLIParity` 逐字段钉死,`kb diff` 补 `--json`);SQLite/PostgreSQL 两后端均可 serve,SIGINT/SIGTERM 优雅退出
- 历史保留水位 `kb gc --keep-last K`:按分支深度只保留最近 K 个快照的检索索引,更早快照仅精简索引(数据本体/树/历史条目保留,`note get --at` 仍可用);fsck 按水位豁免其 Index 引用检查;`search --at` 命中被精简快照给可行动报错;水位持久化 meta,普通 gc 沿用
- Markdown 互操作:`kb export md <目录>`(当前分支或 `--at` 历史快照导出为镜像 .md 文件树,front-matter + 正文原文字节,目标已存在整批拒绝并提示 `--force`)与 `kb import md <目录>`(递归扫描,问题文件整批响亮拒绝,一次提交 + 一次索引增量);roundtrip:export(import(X)) 逐字节一致,import(export(库)) 写回零变更(地址不变)

## v0.3.0 - 2026-09-02

### Added
- 批量导入 `kb bulk import <jsonl>`(JSONL 每行 path/title/tags/body):N 条笔记合并为一次提交 + 一次索引批量增量;压测同语料 2000 条由逐条提交 103s/库 6.7GB 降至 350ms/11MB
- 暂存工作流(借鉴 git):`note set/rm`、`dir rm` 加 `--stage` 进入 `<branch>-stage` 暂存分支累积修改(快照不建索引,单条成本恒定);`kb stage` 查看清单,`kb commit` 以暂存基线为参照生成 main 单快照 + 一次索引增量,`kb commit --abort` 丢弃
- 存储透明压缩(SQLite 后端):indexroot/indexshard 对象写入时 gzip、读取自动解压,库体积 −60%(1.45GB→580MB),地址/哈希/上层语义全透明,`.ckb` 备份可移植性不变;`KB_COMPRESS=off` 实验开关

### Changed
- `note ls` 文本模式走轻量元数据读取(不加载正文),2000 条 316ms→30ms

### Fixed
- 分支推进外键失败转译为可行动提示(「项目不存在,请先 kb project create」),覆盖 note/blob 写入、索引重建、pull、reset 四个写路径

### Docs
- DESIGN §7 入档索引三难权衡定论:写快/读快/历史可复现三者取二,现架构取读快 + 历史可复现;段化与分支缓存两案被否的量化依据与触发条件

## v0.2.0 - 2026-09-01

### Added
- 全文检索 `kb search`(BM25,字段加权:标题 3/标签 2/正文 1;结果确定性可复现,支持 `--at` 历史快照检索与 `--json`)
- 倒排索引纳入快照(新对象 kind:indexroot/indexshard,snapshot.index 引用;结构共享式增量重建)
- `kb index rebuild`:从当前快照全量重建检索索引(旧库升级/自愈用)
- `kb link resolve <slug>`:链接 slug 解析(全路径精确 → 叶名全库唯一 → 歧义列候选)
- `store.ProjectGet` 契约;`project desc <名>` 查询改为点查

### Changed
- **库 schema v5**:objects.kind 约束放宽(新增 indexroot/indexshard);**v4 及更早库需清库重建或走备份恢复升级**(见 docs/upgrade.md)
- `kb restore` 接受 header schema_version ∈ [4, 当前] 的备份(v4 起对象编码兼容),恢复后提示重新 backup 完成升级;v3 及更早与未来版本仍拒绝
- 质量门禁 `verify.sh` 串联端到端 e2e;CLI 集成测试默认 SQLite 零依赖(KB_TEST_DSN 走 PG 回归)

### Fixed
- 升级断点:v0.1.x 备份在新版无法恢复的问题(跨版本恢复门禁放宽)
- `project desc <名>` 查询由全表扫描改为按名点查

## v0.1.1 - 2026-09-01

### Fixed
- dir tree 全库视图与若干稳定性修复(M3.11)

## v0.1.0 - 2026-09-01

### Added
- 首个公开发布:内容寻址存储内核(哈希/对象/存储三层)、知识条目与版本(快照 DAG/diff/回退)、同步与运维(pull/gc/fsck/backup/restore/wipe)、项目隔离、AI 选用元数据、目录层级、SQLite 默认 + PostgreSQL 可选后端、`kb update` 在线自更新
