# cas-kb 总体设计

## 1. 背景与目标

知识库系统的五个工程痛点,与本方案能力一一对应:

| 知识库痛点 | 能力来源 |
|---|---|
| 内容损坏/误改难以发现 | 地址即校验和,读取时免费验证 |
| 重复内容(模板、片段)浪费存储 | 相同内容同地址,天然去重 |
| 版本管理与回溯困难 | 快照 DAG,任意版本可复原 |
| 多端/多人同步成本高 | Merkle 比对,只传输差异对象 |
| 外链失效 | 不可变对象永不 404,slug 解析随版本自洽 |

**非目标(本期)**:全文语义检索(M4 可选)、细粒度权限、离线 P2P 同步。

## 2. 核心思想

```
              Snapshot = H(root + parents + time + msg)   ← 一个哈希代表全库一瞬
                  │
              Tree = H(entries: slug+type → note/子tree)   ← 目录也是内容寻址
                │   │   └── Tree(子目录,M3.8 起可嵌套)
             Note  Note = H(meta + body地址 + links)        ← 条目节点
              │
            Blob(正文原始字节)                               ← 叶子
```

- **内容寻址**:`addr = "sha256:" + hex(sha256(规范字节))`;算法前缀为未来升级 BLAKE3 留门
- **Merkle 结构**:父节点地址由子节点地址决定,任何叶子变动都会向上传播到根
- **唯一可变点**:对象永远不可变;可变状态收敛于命名空间小表——branches((项目, 名字) → 快照地址)与两表的描述列(§4.6)。备份、同步、并发全部归结为管理这两张小表

## 3. 对象模型

八类对象(四类基础 + M4 两类检索索引 + M6-A 两类语义向量),地址都是内容哈希:

| 对象 | 载荷 | 地址含义 |
|---|---|---|
| `blob` | 正文原始字节(无信封) | 内容指纹 |
| `note` | meta + body(blob 地址)+ links | 一条知识条目 |
| `tree` | entries: slug + type(note\|dir)→ 目标地址 | 目录/解析表;type=dir 指向子 tree,目录可嵌套(M3.8) |
| `snapshot` | root(tree 地址)+ parents + time + message (+ index/vec) | 全库一个版本 |
| `indexroot` / `indexshard` | M4 倒排检索索引(根/分片,载荷规格见 §7) | 检索索引随快照冻结、逐快照可复现 |
| `vecroot` / `vecshard` | M6-A 语义向量索引(根/分片,载荷规格见 §7.3) | 向量随快照冻结、按模型版本化 |

snapshot 的两个可选索引字段:`index`(M4,指向 indexroot)与 `vec`(M6-A,指向 vecroot),均为 `omitempty`——未携带时编码与历史逐字节一致,对象地址不变。

### 3.1 note 字段规格

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| kind | 常量 `"note"` | 是 | 类型标签,解码时校验 |
| meta.title | string | 是 | 标题 |
| meta.tags | string[] | 否 | 标签 |
| meta.created_at | int64 | 是 | Unix 秒 |
| body | address | 是 | 正文 blob 地址,格式必须合法 |
| links[] | {slug, rel} | 否 | 关联其他条目;只存 slug 不存地址 |

### 3.2 规范编码(canonical encoding)

- 结构化对象(note/tree/snapshot)以 JSON 编码:**字段声明序即字节序,map 键按字典序输出**
- 同一逻辑对象在任何机器、任何时间编码结果逐字节一致 → 地址稳定
- 字段集合变更 = 磁盘格式变更,**必须升级 `meta.schema_version`**,旧版本数据拒绝混用
- blob 不编码,原样字节

### 3.3 路径与链接解析(版本自洽)

笔记间链接只存人类可读的 **slug**,解析规则:**在当前快照的 root tree 中查 slug → 地址**。

性质:任何历史快照内,链接指向该版本的对象;切换版本,解析随之一致(时间旅行一致性)。改了笔记 A,生成 A′(新地址)+ 新 tree,旧快照里链接依然解析到旧 A——双向链接永不悬空。

**目录层级(M3.8)**:tree 条目带类型(note\|dir),dir 条目指向子 tree,目录可任意嵌套。条目全路径 = 目录段 + slug(如 `go/concurrency/channel`),`/` 是路径分隔符;单段路径即根目录条目,与 M2 扁平用法完全兼容。空目录是合法实体(空 entries 树,沿父链可达,GC 不回收);同目录内 slug 唯一 ⇒ 条目与目录天然不同名。链接 slug 的解析:先按全路径精确匹配;未命中再按叶子名全库唯一匹配,命中多个即报歧义并列出候选——规则确定且随快照自洽。**已实现(M4)**:`repo.ResolveLink/ResolveLinkAt`(后者随指定快照解析,时间旅行一致);CLI `kb link resolve <slug> [--at 快照] [--json]`。

## 4. 存储设计(SQLite 默认,PostgreSQL 可选)

### 4.1 表结构

四张表(完整 DDL 见 [schema.sql](schema.sql);SQLite 后端使用其语义镜像 [schema_sqlite.sql](schema_sqlite.sql),表/列/约束/默认播种/版本一一对应):

- **objects** — `addr(text PK), kind, size, data(bytea)`;只增不删(仅 GC 清扫);全局共享、跨项目去重
- **projects** — `name(text PK), created_at, description`;项目隔离的一等实体(schema v2 新增,v3 加描述列)
- **branches** — `(project, name)(复合主键), addr(→ objects), updated_at, description`;**可变命名空间表(指针+描述)**,按项目划分命名空间
- **meta** — `schema_version` 等键值;加载时版本不符直接拒绝(误配置报错要响)
- 辅助索引:`objects(kind)`、`branches(project)`(schema.sql 尾部;规模小可不建)

> 版本约定:库 schema 版本(meta.schema_version,当前 6)与对象编码版本(object.SchemaVersion,note 内嵌,当前 1)相互独立;v2 仅动表结构,v3 仅加描述列,v4 仅演进 tree 对象编码(带类型条目,表结构不变),v5 放宽 kind 纳入 indexroot/indexshard(M4),v6 再放宽纳入 vecroot/vecshard(M6-A,§4.9)——note/snapshot 编码与地址规则始终不变。

### 4.2 为什么 Postgres 合适

- 分支指针推进需要原子性:单行 UPSERT 天然满足,无需引入锁服务
- 102 主机 Docker 已有运维积累;备份用 pg_dump 即可完整恢复
- `bytea` + 主键足够承载 CAS;未来超大规模可换对象存储后端(store 接口已隔离)

### 4.3 一致性与并发

- 写入流程「读头 → 写对象 → 建快照 → 推进分支」中,对象写入幂等(同地址冲突忽略),失败重试安全
- 并发提交可能丢更新(后写覆盖):MVP 接受 last-writer-wins;乐观并发(提交时声明期望父快照,不匹配则拒绝)列为演进项,本期未实现
- 隔离级别 read committed 足够;分支推进是单行 UPSERT,无复合事务

### 4.4 大内容

- 单 blob 建议上限 16MB(PG 字段与内存友好)
- 超长文档按 64KB 分块、tree 结构挂载(演进项;note.body 预留 chunked 形态,不改地址规则)

### 4.5 项目隔离(schema v2)

- **隔离机制**:分支按项目划分命名空间;每个项目的快照 DAG 由本项目分支头可达,数据隔离由可达性天然获得
- **对象全局共享**:objects 不标注归属,跨项目相同内容共享同一份存储(去重红利保留)
- **默认项目**:default(未指定项目时的作用域);不做旧库自动迁移——库版本不符时拒绝打开,老数据可弃则清库重建
- **项目内解析**:短标识解析限定在当前项目分支头的可达集内,不串项目
- **GC 全局**:对象共享时按项目清扫会误删他项目对象,GC 始终从全部项目的分支头出发标记
- **跨项目搬运**:同库内跨项目 pull 只推进分支指针、零对象传输,充当知识共享通道

### 4.6 AI 选用元数据(schema v3)

- **动机**:AI 的选用链路是「哪个项目 → 哪条分支 → 哪篇条目」。社区通行做法是给可寻址资源配 name + description 的机器可读说明(MCP resources、llms.txt、OCI annotation 皆然);v2 只有名字没有说明,AI 只能拉全文猜测。
- **落点与语义**:projects/branches 各加 description 列(NOT NULL DEFAULT '',约定 ≤512 字符)。描述是**命名空间层元数据**:就地 UPDATE、不产生快照、不进 DAG;对象与地址完全不动;分支推进的 UPSERT 不覆盖既有描述。
- **不变量口径**:「唯一可变状态」由 branches 一张表放宽为 projects/branches 两张命名空间表(指针+描述);对象层仍只增不删。
- **条目层**:note 对象格式不动(地址稳定优先)。AI 粗筛所需摘要由展示层从标题/标签/正文首段**派生**(`kb note ls` 的 JSON 输出),不改对象编码;把 description 写进 note JSON 属对象格式变更,列为演进项,须升级 object.SchemaVersion 并清库重建。
- **发现出口**:`kb project ls --json` / `kb branch ls --json` 输出含描述的机器可读清单,AI 一次调用完成选用。
- **输出契约**:机器消费一律走 `--json`(字段名与结构即契约,调整须在本文档与 ROADMAP 显式记录);文本输出面向人,列格式可能随版本调整——v3 起 `project ls` 文本为「名称/分支数/描述」三列,空描述显示「(未设置)」占位。惯例依据:clig.dev(人类输出为人调优、机器输出走稳定 stdout 通道)与 arduino-cli 向后兼容政策(输出格式变更视为破坏性变更,须显式声明)。v4 起契约变化:`note ls --json` 每行新增 `path` 字段(`slug` 保留为路径叶段);`note get` 文本首列由 `slug:` 改为 `path:`;`diff` 文本与结构化输出的条目键由 slug 改为全路径;新增 `dir ls --json`(每项 name/type/title)。
- **全库树视图(M3.11 增量)**:`kb dir tree` 未显式指定项目(`-p`/`KB_PROJECT` 均未设置)且未给路径参数时,渲染全库文本视图:`(root)/` 下项目为顶层节点,逐项目挂其默认分支(`KB_BRANCH`)的树,无分支的空项目显示 (空);显式指定项目后保持单项目树不变。文本视图面向人;机器消费仍走 `kb project ls --json` 与 `kb note ls --json`。
- **检索与链接(M4 契约)**:`search --json` 每项为 `{path, slug, addr, title, tags, summary, score}`(score 为 BM25 浮点分);`link resolve --json` 为 `{path, slug, addr, title}`;文本输出 search 为「分数 路径 标题」、link resolve 为 path/addr/title 三行。新增命令:`kb search`、`kb link resolve`、`kb index rebuild`(全部支持 `-p` 项目作用域)。

### 4.7 目录层级(schema v4)

- **表结构不变**:v4 只演进 tree 对象编码(entries 从 slug→note 升级为 slug+type,dir 条目指向子 tree,见 §3.3);四张表 DDL 与索引与 v3 逐字节一致
- **整库门禁升至 4**:v3 旧格式 tree 字节(条目无 type)无法通过 v4 解码,实现侧在 DecodeTree 处响亮拒绝;存量库打开时照例拒绝并指引清库重建,不做自动迁移
- **路径定位**:条目按全路径(目录链 + slug)读写,写路径沿目录链 copy-on-write(§6.1);同目录内 slug 唯一,条目与目录天然不同名
- **空目录合法**:空 entries 树沿父链可达,GC 不回收;删除目录下的最后一条目不连带删目录
- **引用一致性**:fsck 校验 tree 条目类型与目标对象 kind 一致(type=note → note 对象,type=dir → tree 对象)

### 4.8 存储后端与分派(M3.10)

- **默认 SQLite**:零外部依赖开箱即用;`modernc.org/sqlite` 纯 Go 驱动(无 CGO,交叉编译产物保持静态)。库文件默认 `~/.local/share/caskb/caskb.db`(`XDG_DATA_HOME` 优先),`KB_DSN` 指定其余路径,`sqlite:` 前缀可省略
- **PostgreSQL 可选**:`KB_DSN=postgres://…` 时切回 pgx/v5 后端;服务端/大库场景延续 §4.2 的运维积累(102 主机)
- **分派口径**:store.Open 按前缀分派——`postgres://`/`postgresql://` → PG,其余一律视为 SQLite 路径;CLI 全部面向 store.Store 接口,repo 层对后端零感知;`kb init` 显示实际后端与目标(展示不含凭据)
- **并发形态**:WAL + busy_timeout(10s)+ foreign_keys ON,经 DSN pragma 逐连接生效;GC/FSCK 的「List 游标遍历中嵌套 Get/Delete」依赖读写并发,WAL 下成立(实测探针选定 pragma 编码形态:参数名字面、值中 `=` 编码为 `%3d`)
- **方言差异**:bytea→BLOB、timestamptz→TEXT(strftime UTC)、addr 正则 CHECK→等价 GLOB+长度 CHECK;SQLite 读回空 blob 可能呈现 nil,Put 侧归一为空 blob(满足 NOT NULL,与 PG 字节语义一致)
- **同一版本门禁**:meta.schema_version 与 PG 共用 6;旧库拒绝打开、`meta` 表在但无版本行等价全新库的口径一致

### 4.9 向量对象与 schema v6 门禁(M6-A)

- **门禁升级**:v6 仅放宽 `objects.kind` 约束(新增 `vecroot`/`vecshard`)并把 meta 播种值升至 6;表结构与 v5 逐字节一致。存量 v5 库照例**拒绝打开**并指引重建(老数据可弃则清库重建,不做自动迁移);v6 实现对不认识的向量编码(载荷 kind 标签不匹配)响亮拒绝,同 M4 先例
- **地址规则**:向量对象地址 = sha256(规范字节);`model` 与 `dim` 写进对象内容,故**跨模型必不同址**——同一批笔记换嵌入模型重建,产出全新地址族,新旧向量族可并存(各自随所属快照可达),不会误混
- **配套继承**:childrenOf 纳入向量对象 → GC/pull/transfer/fsck 零改动继承可达语义;vecshard/vecroot 与 indexshard 同规则受 `gc.keep_last` 水位精简;store 透明 gzip 压缩同待遇(仅 SQLite 后端)

## 5. Go 工程结构(M1–M3.10 按此结构实现)

```
cas-kb/
├── cmd/kb/            CLI 入口:init / note / dir / log / diff / pull / gc / fsck / reset / project / branch / backup / restore / wipe / update / version
├── internal/
│   ├── hash/          地址类型、sha256 封装、格式校验
│   ├── object/        四类对象定义、规范编解码、结构校验
│   ├── store/         存储接口 + postgres/sqlite 实现 + DSN 分派与迁移
│   ├── repo/          业务层:提交、解析、日志、diff、pull、gc、fsck
│   └── selfupdate/    在线自更新:Release 查询、版本比较、产物校验与二进制替换
├── schema.sql         PostgreSQL DDL 规格(权威来源)
└── schema_sqlite.sql  SQLite DDL 镜像(与 schema.sql 同步演进)
```

依赖方向:`cmd → repo → store/object → hash`;store 不依赖 repo;cmd 亦依赖 `selfupdate`(自更新,独立于存储链)。

### 5.1 接口契约(文字规格)

**store.Store**(内容寻址存储 + 分支指针):

| 方法 | 契约 |
|---|---|
| Put(kind, data) → addr | 幂等;同地址重复写等价于空操作 |
| Get(addr) → (data, kind) | 不存在返回哨兵错误 NotFound;kind 必须合法 |
| Has / Delete / List | Has 供遍历跳过;Delete 仅 GC 使用;List 供 GC/FSCK 全量扫描 |
| ProjectCreate(name, description) / ProjectStats / ProjectDescribe | 建项目幂等(已存在等价空操作,可带描述);Stats 返回名称/描述/分支数;Describe 就地更新描述,不产生快照 |
| BranchGet / BranchSet / BranchDelete / BranchList / BranchDescribe | 均按项目作用域(repo 层注入项目);BranchSet 是快照推进的唯一写路径,UPSERT 仅更新 addr/updated_at(**不覆盖 description**),目标对象不存在时报错(FK 兜底);BranchDescribe 就地更新分支描述;BranchList 返回的 BranchRef 含描述 |
| MetaGet / MetaSet / MetaDelete | 库级 KV(meta 表)通用读写删;Get/Delete 键不存在返回 NotFound,Delete 幂等;供 gc 保留水位与合并中间态等机制状态使用,schema_version 等系统键不经过本接口 |
| Wipe | 清空全部业务数据(TRUNCATE 四表)并重跑 schema.sql 播种,等价全新初始化的库;仅供 kb wipe,调用方自负破坏性语义 |
| Close | 释放连接 |

**repo.Repo**(业务层):SetNote / RemoveNote / Note / NoteAt / ListNotes(均按全路径定位条目,M3.8 起)+ Mkdir / RemoveDir / DirLs / DirTree(目录操作)+ Commit / Log / Diff(路径级比较)/ Pull / GC / FSCK + DumpLibrary / RestoreLibrary(整库备份/恢复,M3.9)。写路径沿目录链 copy-on-write:只重写受影响子树,兄弟子树地址结构共享。

**整库备份/恢复(M3.9)**:JSONL 流式格式(header 记 schema_version;对象行含 base64 字节;项目/分支行含描述)。导入时**逐对象重算哈希校验完整性**,损坏文件响亮拒绝;目标库非空默认拒绝(`--force` 先 Wipe 覆盖);恢复完成后建议 fsck 复核。与 §8.3 的 pg_dump 脚本互为补充:原生格式跨后端可移植、无 psql 依赖、自带校验;pg_dump 保留全保真 DB 级备份。

**SQLite 存储透明压缩(压测空间优化)**:索引类对象(indexroot/indexshard——库体积膨胀唯一大头,2000 条逐条写入实测 6.68GB,历史索引占 95%+)在写入时 gzip 压缩(编码:前缀 0x01 + gzip 字节;≥64KB 才压,BestSpeed;地址/哈希/Get 语义全部基于逻辑字节,对 fsck/backup/pull/检索完全透明,备份可移植性不变)。实测(2000 bulk + 300 单写,受控 A/B):库体积 **−60%**(1.45GB→580MB),bulk +11% 时间,单条写 +18ms,检索持平。磁盘敏感场景默认开启;极限写吞吐可 `KB_COMPRESS=off` 关闭(只影响新写入,对既有压缩数据安全)。PostgreSQL 后端依赖 TOAST 压缩,不重复处理。

**跨版本恢复(升级演练结论)**:restore 接受 header schema_version ∈ **[4, 当前]**——对象编码自 v4(带类型 tree 条目)起与当前逐字节兼容,旧备份的每个对象都可原样导入;恢复输出来源版本,低于当前时提示立即重新 backup 完成备份升级。v3 及更早备份(对象编码不兼容)与比当前更新的备份仍拒绝并给出可行动指引。**升级路径**(如 v0.1.x → v0.2.0):①旧版 kb `backup` 导出 .ckb;②新版 kb 对新库(或 `wipe --force` 后)`restore`;③`kb index rebuild` 补建检索索引;④`kb backup` 重新导出完成升级。注意:新版直接打开旧库文件仍被版本门禁拒绝(响亮失败,不做自动迁移),必须走备份恢复路径。

## 6. 关键流程

### 6.1 写入一条笔记(读-改-写)

```
读分支头(可能不存在)
  → 解析条目全路径为目录链 + slug
  → 写正文 blob → 写 note 对象
  → 沿目录链 copy-on-write:叶子目录 entries[slug] = note 地址,
    缺失中间目录自动创建,自底向上写新 tree、替换父条目地址
  → 检索索引增量更新(§7):新旧 tree 叶子无差异 → 复用原索引地址;
    有差异 → 只重写受影响分片(结构共享)
  → 写新 snapshot(parents = [旧头],index = 索引根地址)
  → UPSERT 分支指针
```

全程对象幂等,任何一步失败重试都安全;最坏情况是留下未达对象,交给 GC。

### 6.2 增量同步 pull

1. 读取远端分支头;与本地头相同 → 结束(O(1))
2. 以「本地优先」加载器遍历远端可达对象:本地已有 → 直接用于解码继续下钻;本地没有 → 从远端取回并落库(计数)
3. 祖先检查(判定矩阵,M5 修正「本地领先」误报;T44 增「无共同历史」行):本地头 = 远端头 → 已更新;本地头 ∈ 远端头祖先链 → fast-forward 推进;远端头 ∈ 本地头祖先链 → 已更新空操作;互不为祖先且有共同祖先 → 默认拒绝分叉(提示 `--force` 覆盖或 `--merge` 三方合并),显式 `--force` 覆盖回退,`--merge` 走 §6.3(与 `--force` 互斥);互不为祖先且无共同历史(两库各自 init 的冷启动)→ 默认拒绝但文案分流:提示 `--force` 覆盖,或 `--merge --allow-unrelated` 做空基线合并(T44,不再笼统指路 `--merge` 造成指引断裂);远端项目存在但分支不存在(零提交)→ 「已是最新」空操作(T44,与「本地空拉非空可 ff」对称,本地分支也不存在即双空同;远端项目本身不存在仍响亮报错,防误配静默)

通信量 = 差异对象数 + 路径长度,与库总量无关——百万条笔记改一条,流量约等于一条。

### 6.3 三方合并(pull --merge,M5 已交付)

- **入口与基准**:`kb pull --merge`(与 `--force` 互斥;完整调研见 docs/research/merge-design.md)。基准 = 两分支头在快照 DAG 上的最近公共祖先(沿 parents 链 BFS,不信任 Time;无共同祖先默认响亮拒绝,检出多个候选拒绝并支持显式指定)
- **空基线合并(`--merge --allow-unrelated`,T44)**:两库无共同历史(各自 init 的冷启动)时,显式 `--allow-unrelated` 后以**空树为基准**做三方合并——两侧条目均视为新增,判定完全复用同一判定表(同路径同地址自动合、同路径异地址记 content/type 冲突、单侧存在取单侧),不写第二套判定;零冲突落库、冲突中间态与 `--continue`/`--abort` 收束全部与既有合并路径相同,合并快照 parents=[ours, theirs](空基线无地址,冲突清单 base 列标注「空基线」)。旗标纪律:仅与 `--merge` 连用,单独给或与 `--force` 同给响亮拒绝;有共同祖先时该旗标不改变任何行为
- **条目级三方判定**:按全路径逐 slug 比较 (type, addr) 三元组——单侧变取单侧、双侧同变(同地址)自动合、双侧异变登记冲突;目录递归下钻,Merkle 地址相等即整棵子树剪枝;**不做文本行级合并**(冲突交人工/上层 Agent 裁决,三侧正文经 `note get --at`/`diff` 可读)
- **冲突即全有或全无**:不落正式提交、不动原分支指针,改建显式中间态——`<branch>-merge` 中间态分支(基线快照:树 = 自动合并树,冲突条目取 ours 占位,Message = "merge base",不建索引)+ meta 键 `merge.<项目>.<分支>`(单键 JSON:base/theirs/ours 地址与冲突清单);退出码非零并输出冲突清单(path/kind/base/ours/theirs,kind ∈ content/modify-delete/type)
- **冻结纪律与裁决**:中间态存在期间该分支一切直接写(note set/rm、dir add/rm、bulk import、reset、pull、index rebuild、普通 stage/commit、serve 写端点)响亮拒绝,提示先收束;读操作不受限;`--stage` 升格为裁决动作写入 -merge 视图,`kb stage` 切换为展示裁决清单
- **收束**:`kb merge --continue [-m]` 把「基线 ↔ -merge 头」裁决差异应用到自动合并树 → 索引一次批量增量 → 合并快照(parents = [ours, theirs],历史双侧可达,fsck/GC/pull 传输零改动兼容)→ 推进分支并清理中间态;零裁决拒绝(冲突条目静默保持 ours 占位等于丢 theirs 变更);`kb merge --abort` 删中间态分支与 meta 键回到合并前(孤儿交 GC)。`kb log` 合并行追加第二亲短标识。边界:合并进行中不保证 backup 携带中间态(meta 不在备份载荷);merge --continue 前重打的清单见 `kb stage`/`kb merge`

### 6.4 GC(标记-清扫)

- 从**所有项目**的全部分支头出发做可达性标记(snapshot → tree(含 type=dir 子目录树,递归)→ note → blob,parents 递归);对象共享,禁止按项目清扫
- 未标记对象删除并计数
- 误删保护:GC 清扫前自动导出分支表为 JSON 备份文件(配置项 KB_GC_PROTECT,默认 on;备份失败则中止 GC);MVP 不做 reflog
- 保护分层:CLI 默认开启;repo 库层默认关闭,由调用方显式开启;备份文件不自动清理,由运维按保留策略归档或删除
- **历史保留水位(`gc --keep-last K`,M4 性能批次)**:水位持久化到 meta(`gc.keep_last`);gc 标记时按分支深度计算,深度 ≥ K 的快照只保留本体与数据内容(树/笔记/正文),其**检索索引对象被清扫**——索引是历史体积的大头(每快照冻结全量索引),精简后单写场景库体积大幅下降。被精简快照的数据本体与历史条目全部保留(`note get --at` 仍可用),仅 `search --at` 该快照报友好错误;fsck 按同一水位豁免其 Index 引用检查。`--keep-last 0` 停止精简(已精简的索引不可恢复);默认未设置=全量保留

### 6.5 FSCK

- 全表逐对象重算哈希,与地址比对;按 kind 解码,校验内部引用(body/root/entries/parents 均存在);tree 条目类型与目标对象 kind 一致(type=note → note 对象,type=dir → tree 对象,v4)
- 输出问题清单,发现问题时退出码非零——可直接接 CI 巡检

### 6.6 引用解析

- 命令引用(base/tip 等)按三级解析:精确分支名 → 完整地址(原样采用,存在性在读取时校验)→ 快照地址短标识前缀
- 短标识对全部快照做前缀扫描,命中第二个即提前终止并报歧义,唯一命中才生效
- 规模边界:前缀解析限定当前项目分支头的可达快照集合(项目隔离),集合内线性匹配;超大库建议直接使用完整地址

### 6.7 回退(reset)与历史读取

- **回退 = 指针回拨**:branches 是唯一可变状态,reset 把 (项目, 分支) 指针指回历史快照;目标必须是当前头的祖先,否则拒绝
- **放弃语义**:被放弃的提交变为不可达,下次 GC 清扫;reset 输出被放弃提交数,旧头在 GC 前仍可经完整地址访问
- **历史读取**:note get --at <快照> 按指定快照读条目;短标识解析限定当前项目可达集——被放弃的快照需用完整地址访问(地址即内容,GC 前有效)
- **与 pull 的交互**:回退后本地头是远端头的祖先,再 pull 会被 fast-forward 推回;多机回退需各端一致

### 6.8 暂存工作流(stage→commit,借鉴 git)

- **形态**:`note set/rm`、`dir rm` 加 `--stage` 进入暂存;`kb stage` 查看清单;`kb commit [-m] [--abort]` 提交/丢弃。与「每条即提交」的默认语义并存,显式进入
- **落点**:暂存写入独立分支 `<branch>-stage`(快照不建索引)——单条暂存成本恒定(无索引重写);暂存分支 gc 安全(可达)、随 backup 走、不参与默认 pull
- **基线与差异**:进入暂存时落一个「基线快照」(Message=`stage base`,树=当时 main 树,Index 空);commit 计算 **基线↔暂存** 的叶子差异(这就是用户暂存的变更集,删除有显式 tombstone),应用到**当前** main 树 → 单快照 + 一次索引批量增量 → 删除暂存分支。main 在暂存期间的前进保留(同名路径以暂存为准覆盖;无三方合并)
- **边界**:暂存内容在 commit 前对检索/历史不可见;空目录不支持暂存(`dir add --stage` 拒绝);abort 后暂存快照成孤儿由 gc 清理

### 6.9 Markdown 互操作(export md / import md)

- **形态**:`kb export md <目录> [--at 快照] [--force]` 把当前分支(或 `--at` 指定的历史快照)的全部条目导出为镜像 .md 文件树(条目路径 `go/concurrency/channel` → 文件 `go/concurrency/channel.md`,目录 → 子目录);`kb import md <目录> [-m msg]` 递归扫描目录导入。纯互操作层增量:无数据模型与对象格式变更,两命令均受 `-p`/`KB_PROJECT` 项目作用域约束
- **文件格式**:front-matter + 正文——首行 `---`、第二行 `title: <标题>`、有标签时一行 `tags: a, b`(逗号+空格分隔,无标签省略该行)、再一行 `---`,其后为**正文原文字节**(逐字节保真,不增删换行)
- **export 语义**:先预检全部目标文件,任一已存在即**整批拒绝**并列出冲突文件、提示 `--force`(绝不部分写入);`--force` 整批覆盖;导出空分支得到空目录
- **import 语义**:先解析目录下全部文件再落库——非 .md 文件、非法条目路径(空段 `a//b`、保留段 `.`/`..`、空路径)、中间段是条目(`a.md` 与 `a/b.md` 并存)、front-matter 缺 title / 无法识别的行,均**响亮列出全部问题文件并整批拒绝**;全部合法后走 BulkImport 等价路径:N 条合并为一次提交 + 一次索引增量(不逐条 SetNote)
- **roundtrip 契约**(由测试钉死):export(import(X)) 与 X 逐字节一致;import(export(库)) 写回后 diff 零变更——与当前条目逐字段(title/tags/正文)一致的跳过(地址不变,不产生对象);不一致的若在当前头祖先链上存在内容完全一致的旧条目,则复用其 CreatedAt——内容寻址下同字节必得同地址,故改动/删除后重导也能逐字节还原(地址不变)
- **边界**:front-matter 不承载 links/创建时间等字段,重写条目时 links 会被丢弃(与当前一致的跳过不受影响);`--at` 只读历史、不移动指针

## 7. 检索(M4,已交付 CLI)

索引全部落为 CAS 对象、由快照引用,继承「可复现、可审计、结构共享、跨后端可移植」四性质;store 对索引零感知。

**对象模型(两类新 kind,库 schema v5)**:
- `indexshard`(分片):`{bucket, postings: 词元 → [{a: note 地址, t/g/b: 标题/标签/正文词频}]}`;词元分桶 = FNV-1a % 64,固定片数保证同词元永远同桶
- `indexroot`(索引根):`{version, shards[64](以桶号为下标,空桶空串), docs: [{a, p, l}](文档表:地址→路径与加权长度)}`;文档表按地址排序
- `snapshot.index`(可选字段,`omitempty`):无索引快照的编码与之前逐字节一致(地址不变);旧快照(无索引)检索时报错并指引 `kb index rebuild`

**分词**:Unicode 小写归一 + ASCII 词元(含数字)+ CJK 2-gram(单字成元)+ 其余分隔;输出按字典序,同输入逐字节一致。

**打分**:BM25(k1=1.2, b=0.75),词频与文档长度均为字段加权(标题 3/标签 2/正文 1),权重在查询期套用,调整不需重建索引;多词 OR 归并。**确定性排序:分数降序 → 路径升序 → 地址**——同一快照同一查询结果与顺序完全一致(ROADMAP M4)。

**写路径**:commitTree 内按新旧 tree 的叶子差异增量重建——无差异复用原索引地址;有差异只重写受影响桶(先载旧分片再作减法/加法,不丢同桶其他笔记);结构无变化的桶产出相同地址,天然结构共享。`childrenOf` 纳入索引对象,GC/pull/fsck/backup 零改动继承。

**CLI**:`kb search <query...> [--at 快照] [-n N] [--json]`;`kb index rebuild` 从当前快照全量重建(自愈,亦用于旧库升级)。

**空库与无索引契约**:空库(无任何提交)检索返回无结果——与 `note ls` 的「(no notes)」对齐;有提交但快照无索引(M4 之前的旧数据)检索报错并指引 `kb index rebuild`。该契约由 `TestM4_SearchContract` 钉死。

**与原设计的差异**:原稿设想「每分片 = 小 tree」,落地为独立两类 kind——tree 条目的 type(note|dir)语义不匹配倒排项,独立 kind 让 childrenOf/fsck 的 kind 一致性校验保持精确;schema v5 门禁如约升级(原 §7 预案)。语义向量检索同法处理(IVF 聚类分片),列为演进项。

**设计权衡(如实记录)**:
- `indexroot.docs` 的 path/len 是**派生缓存**,权威事实来源永远是快照 root tree;缓存丢失或损坏可由 `kb index rebuild` 全量重建,正确性不依赖它
- commit 内索引增量需加载旧快照与旧 tree 并收集叶子,单次提交成本 O(N)(N=条目数)——知识库量级(N ≤ 数万)无感;若未来写路径成为瓶颈,可改为由变更集直接驱动(调用方传入 diff),接口已预留
- 量级判断:该方案目标 ≤ 十万条;此前已论证该量级下精确遍历/全量分片优于 ANN(可复现 + 结构共享 + 双后端对称),勿提前优化

**版本号演进规则(两套门禁各管一域)**:
- `object.SchemaVersion`(note meta 内嵌):**对象编码**字段/语义变更时升级——旧字节无法按新结构解码的场合;升级即意味旧对象不可读,需清库重建(v3 tree 条目加 type 即是)
- `DBSchemaVersion`(meta 表):**库表 DDL**或跨对象不变式变更时升级(加列、加表、放宽 kind 约束);升级即拒绝旧库打开,指引清库重建(v3 加 description 列、v5 放宽 kind 即是)
- 仅追加「可选字段 + omitempty」且新旧解码双向兼容的演进,两者都可不动(v5 的 `snapshot.index` 即是,旧快照编码逐字节不变)

**已知写放大与批量导入(压测结论,2000 条中文语料实测)**:
- 单条 SetNote 的索引增量会重写「受影响分片」,而一篇笔记的词元(中文 2-gram 数百个)散布在几乎全部 64 桶 → 实际重写≈全索引(MB 级):写入耗时随库规模线性退化(2000 条时 95ms/条,累计 103s),且每个历史快照冻结其全量索引 → 库体积 O(N²)(实测 6.68GB;GC 清扫 0——历史索引随快照可达,是可复现性的设计代价)
- **批量导入 `kb bulk import`(推荐灌注方式)**:N 条合并为一次提交+一次索引批量增量——2000 条实测 **350ms**(约 295 倍),库 11.1MB(约 600 倍),检索持平
- 量级指引:交互式单条写入 ≤ 数千条(约 100ms/条封顶);AI 批量灌注/初始化一律用 `kb bulk import`;更大规模(≥数万条)需索引段化(文档分区,演进项)——彼时写放大 ÷段数,历史索引共享相邻快照
- 读路径不受影响(实测 2000 条:get≈0ms、ls 316ms、tree 34ms、search 46-58ms、log 55ms)

**索引三难权衡定论(写快 / 读快 / 历史逐快照可复现,三者只取其二)**:
- 现架构取「**读快 + 历史可复现**」:词分区查询只读命中分片(实测 46ms),索引随快照冻结保证逐快照可复现;代价是单条提交重写≈全索引(写慢,已由 `kb bulk import` 绕开)
- 被否方案 A(文档分区段化):单条写恒定 ~15-20ms,但查询不知词在何段、必须读全索引(2-gram 下索引≈语料 30 倍,2000 篇即 ~30MB/查询)→ 检索慢 5-10 倍且随语料线性恶化。触发条件:真实 workload 变为「写多读少」且可接受检索劣化
- 被否方案 B(索引降级为分支级缓存):写 O(1)+读快,但牺牲 `--at` 历史快照检索与逐快照可复现——动摇本设计核心卖点。触发条件:用户明确不需要历史检索
- 结论:**维持现状**;两案的量化依据与触发条件如上,无新 workload 证据不重启讨论

### 7.1 片段高亮(snippet,M4.2 展示层增量)

**定位**:纯展示层——检索的评分/排序/命中集合零变化;片段在结果序列确定之后逐条附加。「同一查询带/不带 --snippet 的结果序列(路径+分数)完全一致」由排序不变断言双侧钉死(cmd/kb TestSearchSnippetCLI、internal/server TestServeSearchSnippet)。

**契约变化(均为可选,缺省输出与旧契约逐字节一致)**:
- CLI:`kb search` 增布尔旗标 `--snippet`;文本模式每条命中行下追加一行缩进片段(4 空格),命中词元以【】包裹;`--json --snippet` 增可选字段 `"snippet"`(`omitempty`,不带旗标时字段整体不存在,旧消费者零破坏)
- HTTP:`GET /api/v1/search` 增可选查询参数 `snippet=1`(仅字面 1 生效,其余取值视为缺省),JSON 同 `"snippet"` 字段,语义与 CLI 相同
- 两条出口复用 internal/view 一份实现(`view.Snippet` / `view.SearchRowsWithSnippet`),行为逐字段一致(TestServeCLIParity 扩展钉死)

**算法(确定性,可复现;不读时钟、不用随机)**:
1. 查询经与索引同一套分词(`index.Tokenize`)得到词元——片段标记与 BM25 实际匹配的词元同口径;
2. 正文按 rune 做 Unicode 小写归一后扫描(Go 的 ToLower 为 1:1 rune 映射,区间可直接用于原文):ASCII 词元按整词相等命中(词边界,chan 不命中 channel);CJK 2-gram 在 CJK 连续段内取全部子串出现,孤字词元仅命中段长为 1 的孤字(与索引「单字成元」同语义);命中区间合并(重叠/相接),2-gram 因此扩展回完整词源(查询「知识库」→ 词元「知识」+「识库」→ 标记【知识库】而非【知识】【识库】);
3. 以任一词元的首次出现为中心截取窗口:目标 80 rune(前约 40、后约 40),按 rune 切、不劈开多字节字符;发生截断的边缘向内最多回望 20 rune 吸附到分隔符(标点/空白,与分词「其余字符作分隔」同口径,截在分隔符之后),首个命中必须完整可见;
4. 窗口内全部命中区间以【】包裹(与窗口边缘相交的区间按窗口裁剪)。

**无正文命中契约(二选一,选定并钉死)**:无任何词元命中 body(如仅标题命中)时,片段取 body 开头同等窗口(80 rune)、无标记;body 为空则片段为空串(`omitempty` 下字段同样缺省)。由 `TestSnippetNoBodyHit`(internal/view)与 e2e serve 段(q=H12 标题命中 → `"snippet": "b12"`)钉死。

**开销**:O(正文长度) 的 rune 扫描,仅发生在带 `--snippet` / `snippet=1` 的请求路径;默认检索路径零改动。

### 7.2 段化观测清单(草案,T47)

> 来源:docs/research/best-practices-adoption.md §4(评审采纳)——观测面模板取自 bleve scorch 的 Stats 结构(gauge/counter 分类、根段数、merge 写字节、最慢 merge 耗时),变量集取自 tantivy LogMergePolicy(每段文档数 + 删除比,对应本库「索引对象数/库体积」与「无效占比」)。本清单只回答「看什么」,不设计任何实现、不改上文三难定论;采集脚本属压测/观测工具,不入 verify 门禁。

段化(被否方案 A)的触发条件上文已定性,但「靠什么数据判定已到触发线」此前无定义。以下六指标均可由现成命令/压测脚本采集,无需改产品;指标 1/3/5 是**决策指标**(直接对应段化触发条件),2/4/6 是**护栏指标**(防止在别的轴上悄悄恶化):

| # | 指标 | 口径 | 采集点 | 触发线(线索,非承诺) |
|---|---|---|---|---|
| 1 | 单条写延迟 P99 | `note set` 端到端耗时,按库内条目数分桶记录 | 压测脚本(循环 + 计时);对照本章实测基线(2000 条 = 95ms/条) | P99 随条目数线性外推至交互不可接受(本章定性线:约 100ms/条封顶) |
| 2 | 库体积及增速 | 库文件/PG 库占用,分「数据对象 vs 索引对象」两列(按对象 kind 聚合 size) | `kb fsck` 扩展输出或一次性统计查询;压缩后口径 | 历史索引占比 > 80% 且绝对体积进入运维红线 |
| 3 | 单次写索引重写字节 | 每次单条写实际重写的 indexshard 字节(≈写放大) | 压测脚本从 store 层统计;或 GC 前后对象计数差 | 恒定接近全索引字节(本章实测:单条写≈全索引)即触及「写慢」极值 |
| 4 | 索引对象数 / 快照 | indexshard 对象数 × 快照数(gauge,bleve TotFileSegmentsAtRoot 同型) | objects 按 kind 计数 | 对象数增速显著超快照数增速(结构共享失效信号) |
| 5 | bulk 吞吐与单条写的比值 | bulk import N 条耗时 ÷ N vs 指标 1 | 压测脚本(2000 条基线:350ms 全批 vs 95ms/条) | 比值持续扩大 = bulk 缓解失效,段化收益上修 |
| 6 | 检索延迟 P95(含 --at) | search 端到端,现快照与历史快照分开记 | 压测脚本(基线 46-58ms) | 段化方案的已知代价(读全索引 5-10 倍劣化)出现前先有基线 |

两条纪律:(a) 决策指标(1/3/5)与护栏指标(2/4/6)分开看——护栏恶化只说明要调优或 gc,不构成段化立项依据;(b) 全部指标记录口径于本文,采集脚本属压测/观测工具,不入 verify 门禁。

**声明**:上文「三难权衡定论」维持原文——**无新 workload 证据不重启三难讨论**;本清单的价值恰是「到了触发线时有据可查」,而非提前立项。

### 7.3 语义检索(规划中,M6-B 实现检索面)

**现状**:M6-A(T54)已交付**向量对象模型与嵌入重建**——向量以 CAS 对象入库、随快照冻结;**检索面(查询嵌入、余弦相似、与 BM25 的融合/切换)属 M6-B,本批未做**;默认检索仍是 BM25,`kb search` 行为零变化。

**四条红线(负责人裁决,不可越)**:
1. **不内嵌模型运行时**:嵌入一律走外挂 HTTP 服务(internal/embed,Ollama 兼容 `/api/embed`);CLI 进程不加载任何模型权重
2. **不引入向量数据库**:向量就是内容寻址对象(vecshard/vecroot),存储/GC/备份/同步全部继承现有 CAS 机制,不新增外部依赖
3. **向量按 model_id 版本化入内容**:`model`/`dim` 写进 vecshard/vecroot 内容,跨模型必不同址;混版由 fsck 检出
4. **本批次不做检索集成**:M6-A 只落对象与重建;检索 API/排序/融合留待 M6-B 单独评审

**对象模型(两类新 kind,库 schema v6,见 §4.9)**:
- `vecshard`(分片):规范 JSON `{kind, model, dim, items: [{path: 全路径, vec: base64}]}`;字段定序、items 按路径排序;`vec` 为全部 float32 分量按 **little-endian** 拼接后 base64(StdEncoding)的单个字符串——二进制承载规避 JSON 浮点文本的精度/格式歧义,保证跨平台逐字节确定
- 分桶 = FNV-1a(条目全路径) % 64,与 indexshard 同构分片;桶键是路径(向量项的寻址主体是笔记),indexshard 的桶键是词元
- `vecroot`(根):`{kind, model, dim, shards[64]}`(桶号下标,空桶空串,照 indexroot 范本);root 的 model/dim 是 fsck 一致性基准
- `snapshot.vec`(可选,`omitempty`)指向 vecroot;无向量快照编码逐字节不变

**Embedder 契约(internal/embed)**:`Model()/Dim()/Embed(ctx, texts) ([]vector, error)`;Ollama 适配器 POST `{KB_EMBED_URL|http://127.0.0.1:11434}/api/embed`,请求 `{"model": KB_EMBED_MODEL, "input": [texts]}`,读 `embeddings` 数组(批量语义:每输入串一维、顺序一致——字段名与批量语义经官方文档 docs/api.md 核实并注释于代码);HTTP 超时 30s;**KB_EMBED_MODEL 未设置 = 向量功能整体关闭,入口给可行动报错,绝不静默跳过**。

**重建路径**:`kb index rebuild --embed`——逐条笔记(标题+空行+正文)嵌入、按桶聚合写分片与根、快照带 vec 落库(tree 未变故结构共享,BM25 索引地址沿用);嵌入失败响亮中止、分支指针不动(对象幂等可重试)。普通内容提交不带 vec(向量仅描述重建时刻的库),重跑 rebuild --embed 恢复;普通 `kb index rebuild` 反向沿用头快照 vec。**向量重建是显式操作**,不进写热路径。

**运维配套**:fsck 校验分片 model/dim 与根一致、items 路径存在于对应快照(缺失=fail;无 vec 快照跳过不报);GC 对 vecroot/vecshard 与 indexshard 同规则可达回收(`gc.keep_last` 水位同精简);vecshard/vecroot 走 store 透明 gzip 压缩(仅 SQLite)。

**M6-B 待决(届时评审,不在本批)**:查询端点形态(独立 `kb vsearch` vs 融合进 `kb search`)、Top-K 与阈值、与 BM25 的融合策略、暴力扫描的量级上限判断——量级与三难关系沿用 §7 定论(≤十万条量级精确遍历优于 ANN,勿提前优化)。

## 8. 部署与配置

### 8.1 拓扑

- 个人/开发:**默认 SQLite 本地文件**(`~/.local/share/caskb/caskb.db`),零部署零依赖
- 生产:PostgreSQL 16 Docker 部署于主机 `102`,库名 `caskb`;Go CLI/服务端同内网访问(`KB_DSN=postgres://…` 接入)
- 开发(PG 回归):本机 `docker compose` 起同构实例(compose 片段见 README 附录)

### 8.2 配置项

| 变量 | 默认 | 说明 |
|---|---|---|
| KB_DSN | `sqlite:~/.local/share/caskb/caskb.db` | 库连接串:SQLite 文件路径(`sqlite:` 前缀可省略;`:memory:` 为内存库)或 `postgres://…`(切换 PostgreSQL 后端) |
| KB_BRANCH | `main` | 默认分支 |
| KB_GC_PROTECT | `on` | GC 清扫前自动导出分支表备份;设为 off/0/false 关闭 |
| KB_REMOTE_DSN | (无) | `kb pull` 的远端连接串(也可作为命令行参数传入) |
| KB_TEST_DSN | (无) | 集成测试基库连接串;未设置时跳过集成测试(仅测试使用) |
| KB_PROJECT | `default` | 项目作用域:note/log/diff/gc/fsck 等命令只作用于该项目;亦可用 -p 按命令覆盖 |
| KB_UPDATE_REPO | `imeepos/cas-kb` | `kb update` 检查的 GitHub 仓库(owner/name),也可 --repo 按次覆盖 |
| GITHUB_TOKEN | (无) | 可选;GitHub API 令牌,缓解匿名限流(仅 update 使用,只作请求头) |
| KB_EMBED_MODEL | (无) | 语义向量嵌入模型名(M6-A,如 nomic-embed-text);**未设置 = 向量功能整体关闭**(`kb index rebuild --embed` 给可行动报错) |
| KB_EMBED_URL | `http://127.0.0.1:11434` | 嵌入服务地址(Ollama 兼容 /api/embed);向量按 model 版本化入内容,换模型须重跑 rebuild --embed |

指向 102 的示例(PG 后端):`postgres://caskb_app:<密码>@192.168.x.102:5432/caskb?sslmode=disable`。
安全要求:专用账号 `caskb_app`(只授 caskb 库权限)、密码走 scram-sha-256、内网传输是否启用 TLS 按内网策略定;**凭据一律走环境变量,不入库不入仓**。

### 8.3 备份与恢复(正规化)

- **两条备份路径**:① `kb backup [文件]` 原生导出(.ckb,JSONL;跨后端可移植、无 psql 依赖、恢复时逐对象校验哈希);② `./scripts/backup.sh [DSN]` pg_dump 全保真 DB 备份(仅 PostgreSQL 后端;产物写 `backups/`,git 忽略,文件名含库版本与时间戳并附 sha256)。两者都是全库语义
- **跨后端迁移**:.ckb 与后端无关——旧后端 `kb backup` 导出、新后端 `kb restore` 导入,即完成 SQLite↔PostgreSQL 双向迁移;e2e 双模式覆盖两种后端的完整生命周期
- **恢复**:`kb restore <文件> [--force]`(非空库需 --force,先 Wipe)或 `./scripts/restore.sh <backup.sql> <目标库>`(导入全新库;旧 schema 备份会提示配套二进制)
- **备份统一出口(脚本)** `./scripts/backup.sh`:pg_dump 逻辑备份(含 objects/projects/branches/meta),文件名 `caskb-v<库版本>-backup-<时间戳>.sql`——版本号进文件名,恢复时可识别配套的 kb 二进制
- **恢复统一入口** `./scripts/restore.sh <backup.sql> <目标库>`:导入**全新库**(先删后建);备份属于旧 schema 时打印提醒(v4 门禁会拒绝旧库,需用对应版本的 kb 二进制访问)
- **迁移口径**:不做自动迁移(库版本不符拒绝打开)。官方升级路径 = backup.sh 留档 → 清空/删库 → `kb init` 重建 → 数据按需重录或写一次性迁移工具;`meta 表存在但无版本行`(清空后)等价全新库放行
- **硬性约定**:对含数据的存量库做任何迁移/升级验证前,必须先 backup.sh(或 pg_dump)备份,文件名含库版本与时间戳
- branches 表极小可高频快照;objects 只增,增量备份友好
- 恢复后先跑 fsck 再提供服务

### 8.4 CLI 在线更新(kb update / kb version)

- **版本号来源**:发布流水线以 `-ldflags "-X main.version=<tag>"` 注入;本地 `go build` 未注入时为 `dev`,`kb version` 打印版本与平台
- **检查**:`kb update` 请求 GitHub API `repos/<仓库>/releases/latest`(不含 draft/prerelease),与当前版本逐段比较:数字段按数值,同数值带后缀(预发布)小于无后缀,缺段按 0 补齐;`dev` 构建只展示最新版不比较;已是最新则提示并正常退出
- **升级**:`kb update --yes` 下载当前平台产物 `kb-<版本>-<os>-<arch>.tar.gz`(Windows 为 `.zip`)与 `sha256sums.txt`,边下边算 sha256,比对通过才从归档解出二进制,经「target→.old、新→target、删 .old」的 rename 序列原子替换;任一步失败保留原二进制。临时文件写在本二进制同目录(需目录写权限)
- **仓库与限流**:默认 `imeepos/cas-kb`,`--repo owner/name` 或 `KB_UPDATE_REPO` 覆盖;匿名 API 有限流,可设 `GITHUB_TOKEN`(仅作请求头,不入盘不入库)
- 更新只替换 CLI 本体,不触碰库 schema;版本升级涉及的库 schema 演进仍按 §8.3 迁移口径处理

### 8.5 只读 HTTP API(kb serve,M4 收尾;写入端点见 §8.6)

- **定位**:让 AI/Agent 与外部工具免 shell 消费知识库;`kb serve [--addr 127.0.0.1:8787] [-p 项目]` 启动,`KB_DSN`/`KB_BRANCH`/`KB_PROJECT` 正常生效,SQLite 与 PostgreSQL 两后端都可 serve;SIGINT/SIGTERM 优雅退出(停收新请求、排空在途请求,默认 5s)
- **端点表**(全部 GET;响应 JSON,2 空格缩进、不转义 HTML,与 CLI `--json` 同款编码):

| 端点 | 参数 | 语义 | 错误 |
|---|---|---|---|
| `/healthz` | — | 探活:`{"ok":true,"backend","schema_version","project"}` | 500 |
| `/api/v1/projects` | — | 项目清单,`project ls --json` 同构 | 500 |
| `/api/v1/tree` | `at`(短标识/分支名,省略=分支头) | 当前项目层级树(嵌套:dir 带 children,note 带 addr/title) | at 不存在 404;歧义 400 |
| `/api/v1/note` | `path` 必填,`at` | 单条笔记(正文原文 + 派生摘要,tags 归一 `[]`) | 缺/坏 path 400;类型冲突 400;不存在 404 |
| `/api/v1/search` | `q` 必填,`at`、`limit`(正整数) | BM25 检索,`search --json` 同构;limit 只截断不重排 | 缺 q / limit 非法 400;at 不存在 404 |
| `/api/v1/log` | `limit`(正整数) | 快照链(最新在前):id/time/message/parents,短标识与 CLI 同长 | limit 非法 400 |
| `/api/v1/diff` | `from`、`to` 必填 | A/D/M 按全路径,`diff --json` 同构 | 缺参 400;引用不存在 404;歧义 400 |
| `/api/v1/merge-state` | `project`、`branch`(省略=serve 作用域与默认分支) | 合并中间态查询(T48),`kb stage --json` 同构:`state ∈ idle\|merging` + 派生布尔 `can_continue`/`can_abort` + 事实字段 `base`/`theirs`/`ours`/`conflicts[]`/`conflict_count`/`merged_branch`;idle=200 轮询稳态(事实字段 null、conflicts 空数组、两布尔 false),合并态事实取中间态 meta 键,conflicts 与 CLI 冲突清单同契约 | 项目/分支不存在 404;参数空白 400 |

- **只读纪律(§8.6 增量前)**:`/healthz` 与 `/api/v1/{projects,tree,note,search,log,diff,merge-state}` 全部只读,非 GET(含 POST)一律 405 + `Allow: GET`;未知路径 404;错误响应一律 `{"error":"…"}`(400 参数问题 / 404 目标不存在 / 500 其余)。§8.6 之后写路径有 CLI 与写入型 HTTP API 两条,读端点纪律不变
- **契约一致性声明**:JSON 行契约集中在 `internal/view`(字段名、字段序、摘要与短标识派生规则一份实现),CLI `--json` 与 `/api/v1/*` 共用;`cmd/kb` 的 `TestServeCLIParity` 在同一临时库上对 search(含多词、`--at`、limit)、projects、diff 逐字段断言两条出口相等,顺序亦必须相等;`GET /api/v1/merge-state` 与 `kb stage --json` 共用 `view.MergeStateRow` 一份契约,由 `TestServeMergeStateParity` 同法钉死(T48,merge-design §4-9 开放问题闭合)
- **安全边界**:默认只绑 `127.0.0.1`,不对外网暴露;跨机消费走 SSH 端口转发或反向代理(自行加鉴权);`--addr 127.0.0.1:0` 由内核分配端口,测试与 e2e 用其避免端口冲突

### 8.6 写入型 API 与令牌鉴权(kb serve 写端点,M4.1 增量)

- **定位**:让 AI/Agent 经 HTTP 直接写知识库,不必落 shell;在 §8.5 只读 API 之上新增**恰好两个**写端点(POST/DELETE /api/v1/note),不暴露 stage/bulk/其他写命令(范围控制)。写语义与 CLI 逐字段一致——直接复用 repo.SetNote/RemoveNote,不写第二套逻辑
- **令牌语义**:`--token <值>` 旗标与环境变量 `KB_SERVE_TOKEN`(旗标优先)配置写入令牌;令牌只在内存中与请求头比较(crypto/subtle.ConstantTimeCompare),**不写日志、不回显**。配置后读端点保持无鉴权(本机约定,§8.5 不变);写端点要求 `Authorization: Bearer <token>`,缺失或错误分别 401,全程不在响应中回显 token
- **只读降级(安全底线)**:未配置令牌时服务保持纯只读,一切行为与 v0.4.0 完全一致;两个写端点存在但一律 `403 + {"error":"服务未配置写入令牌,当前为只读模式;设置 KB_SERVE_TOKEN 后启用"}`
- **端点表**:

| 端点 | 入参 | 语义 | 错误 |
|---|---|---|---|
| `POST /api/v1/note` | body JSON `{"path","title","tags"?,"body"}` | 等价 `kb note set`;成功 `201 + {"path","address","short"}`(address=note 对象地址,short=新快照短标识) | 参数缺失/非法路径/路径是目录 400(沿用 CLI 可行动文案);缺/错令牌 401;未配置令牌 403;锁忙 503 |
| `DELETE /api/v1/note` | `?path=<全路径>` | 等价 `kb note rm`;成功 `200 + {"removed":1,"short"}` | 路径不存在 404;缺/坏 path 400;缺/错令牌 401;未配置令牌 403;锁忙 503 |

- **503 语义(并发与一致性)**:serve 与 CLI 同时写依赖后端事务串行化(SQLite 单写者 + busy_timeout / PG 行锁),后端报锁忙(SQLITE_BUSY / PG lock 类错误,store.IsLockBusy 识别)时返回 `503` + 可行动提示「稍后重试或改用 CLI」;写路径对象幂等,失败只留未达对象交 GC,**不产生半写状态**
- **写后不变量**:每次 POST/DELETE 在响应前完成「blob/note/tree/snapshot + 检索索引增量 + 分支指针推进」全链路(repo.SetNote/RemoveNote 同步语义),因此响应返回后 fsck 恒可过、检索立即可见——由 TestServeWriteReadback(POST 后 fsck 零问题)与 TestServeWriteSearchImmediate(POST 后立即可检索)钉死
- **契约一致性**:写响应与 GET /api/v1/note 的行契约同在 internal/view 体系(短标识派生同源 view.ShortAddr);`kb note get` 顺带补 `--json`(输出 view.NoteRow,与 GET /api/v1/note 同构),由 cmd/kb `TestServeWriteCLIParity` 钉死:API POST→CLI 读回逐字段相等、CLI set→API 读回逐字段相等、API DELETE→CLI 报不存在
- **测试与验收**:`go test ./internal/server/ -run TestServeWrite -v`(鉴权矩阵/读回/立即可检索/非法路径/删除/锁忙 503)、`go test ./cmd/kb/ -run TestServeWrite -v`(CLI parity)、e2e 写 API 段(KB_SERVE_TOKEN 起服 → POST → search → CLI get → DELETE → CLI 确认;无令牌实例 POST 403)

### 8.7 健康自检(kb doctor,T49)

- **形态**:`kb doctor [--json] [--check <name>…] [-l|--list-checks] [-p 项目]`。无参跑全部检查,逐项输出「ok/warn/fail + 一句人话 + 可行动修复建议」,末尾汇总行 `doctor: N ok, M warn, K fail`;**退出码两档:有 fail ⇒ 1,仅 ok/warn ⇒ 0**(warn 不拦 CI,学 brew doctor 的克制;细分只存在于行内与 --json)。`-l|--list-checks` 按注册表顺序列举检查名——**检查名即契约,新增只在表尾追加,不破坏旧名**;`--check <name>` 可多次给,单独跑指定项(输出顺序仍按注册表);`--json` 输出 `[{check,status,detail}]` 数组,行契约在 internal/view.DoctorRow(与文本出口同源)
- **检查项(v1 六项,全部复用现成能力,零新增诊断逻辑;doctor 只诊断不施治,修复永远指向可行动命令)**:

| 检查名 | 内容 | 状态映射 |
|---|---|---|
| storage | 打开 KB_DSN 后端(与其它命令同口径,含迁移)+ 库 schema 门禁 | 打不开/版本不符 = fail |
| fsck | 等价 `kb fsck`(repo.FSCK);另以 repo.UnreachableCount 只读统计悬垂/未达对象(标记口径与 GC 完全一致,同一套逻辑的第二出口) | 完整性问题 = fail;悬垂/未达对象 = warn(信息非错误,学 git fsck --dangling;下次 gc 会清扫) |
| version | `kb version` 本体 | 仅信息,永不 fail;dev 构建注明「未注入版本号」(沿 §8.4 口径) |
| config | 已设置的 KB_* 环境变量逐个核对:DSN 形态(KB_DSN/KB_REMOTE_DSN/KB_TEST_DSN)、KB_PROJECT 存在性、KB_GC_PROTECT 取值、KB_UPDATE_REPO 形态;令牌类变量只报「已设置」 | 形态非法 = fail;目标不存在/取值不可识别 = warn;未设置的默认项不提 |
| gc-protect | KB_GC_PROTECT 开关态 + 分支表备份目录(当前工作目录,见 §6.4)可写性 | 不可写 = warn |
| serve | 探活 `127.0.0.1:8787` 的 `/healthz`,核对 backend 与 schema_version 和本工具一致 | 连接拒绝 = ok(明确「未运行」不是错误);可达但不一致/不健康 = warn |

- **凭据纪律**:doctor 全部输出(文本与 --json 的 detail)**绝不回显连接串凭据段**——后端展示统一走 store.DescribeBackend(PostgreSQL 只回显 host/database,SQLite 只回显文件路径),DSN 非法的报错丢弃 url.Parse 原文换语义说明;令牌(KB_SERVE_TOKEN/GITHUB_TOKEN)只报「已设置(值不回显)」
- **存储不可用的联动**:storage fail 时,依赖存储的 fsck/config(存在性部分)不再重复打开,给可行动说明并同样记 fail——doctor 的退出码始终由「有无 fail」决定

## 9. 风险与权衡

| 风险 | 应对 |
|---|---|
| 规范编码字段演进破坏地址稳定性 | 字段变更必须升 schema_version;地址带算法前缀留升级门 |
| 并发丢更新 | M2 乐观并发(期望父快照校验) |
| PG 单机容量上限 | store 接口隔离可换对象存储后端;大 blob 分块 |
| 敏感内容明文入库 | 演进:对象级加密后入库,服务端只见密文 |
| GC 误删未达历史 | 保护窗口 + 备份先行;GC 输出明细可审计 |

## 10. 术语表

| 术语 | 含义 |
|---|---|
| CAS | 内容寻址存储,地址 = 内容哈希 |
| Merkle 树 | 父节点哈希由子节点哈希构成的树,根哈希代表整体 |
| 快照 snapshot | 全库一个版本的命名(root tree 地址) |
| slug | 人类可读的条目名(路径叶段),链接解析的键 |
| 目录 dir | tree 条目的类型之一,指向子 tree;目录可嵌套,空目录合法(路径 = 目录链 + slug) |
| 项目 project | 知识库的命名空间单位;分支与版本按项目隔离,对象跨项目共享去重 |
| fast-forward | 本地头是远端头的祖先时,直接推进分支无需合并 |
| 3-way merge | 基于共同祖先的三方合并 |
