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

四类对象,地址都是内容哈希:

| 对象 | 载荷 | 地址含义 |
|---|---|---|
| `blob` | 正文原始字节(无信封) | 内容指纹 |
| `note` | meta + body(blob 地址)+ links | 一条知识条目 |
| `tree` | entries: slug + type(note\|dir)→ 目标地址 | 目录/解析表;type=dir 指向子 tree,目录可嵌套(M3.8) |
| `snapshot` | root(tree 地址)+ parents + time + message | 全库一个版本 |

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

> 版本约定:库 schema 版本(meta.schema_version,当前 4)与对象编码版本(object.SchemaVersion,note 内嵌,当前 1)相互独立;v2 仅动表结构,v3 仅加描述列,v4 仅演进 tree 对象编码(带类型条目,表结构不变)——note/snapshot 编码与地址规则始终不变。

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
- **同一版本门禁**:meta.schema_version 与 PG 共用 4;旧库拒绝打开、`meta` 表在但无版本行等价全新库的口径一致

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
| Wipe | 清空全部业务数据(TRUNCATE 四表)并重跑 schema.sql 播种,等价全新初始化的库;仅供 kb wipe,调用方自负破坏性语义 |
| Close | 释放连接 |

**repo.Repo**(业务层):SetNote / RemoveNote / Note / NoteAt / ListNotes(均按全路径定位条目,M3.8 起)+ Mkdir / RemoveDir / DirLs / DirTree(目录操作)+ Commit / Log / Diff(路径级比较)/ Pull / GC / FSCK + DumpLibrary / RestoreLibrary(整库备份/恢复,M3.9)。写路径沿目录链 copy-on-write:只重写受影响子树,兄弟子树地址结构共享。

**整库备份/恢复(M3.9)**:JSONL 流式格式(header 记 schema_version;对象行含 base64 字节;项目/分支行含描述)。导入时**逐对象重算哈希校验完整性**,损坏文件响亮拒绝;目标库非空默认拒绝(`--force` 先 Wipe 覆盖);恢复完成后建议 fsck 复核。与 §8.3 的 pg_dump 脚本互为补充:原生格式跨后端可移植、无 psql 依赖、自带校验;pg_dump 保留全保真 DB 级备份。

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
3. 祖先检查:本地头 ∈ 远端头祖先链 → fast-forward 推进分支;否则报告分叉(强制覆盖需显式 `--force`)

通信量 = 差异对象数 + 路径长度,与库总量无关——百万条笔记改一条,流量约等于一条。

### 6.3 合并(演进项,本期未实现)

- 共同祖先 = 快照 DAG 的最近公共祖先
- tree 级 3-way:双方地址相同 = 未改;单方改 = 取改方;双方改且不同 = 冲突,升级到条目级人工裁决
- 合并结果 = 新快照(parents = [本地头, 远端头]),历史无损

### 6.4 GC(标记-清扫)

- 从**所有项目**的全部分支头出发做可达性标记(snapshot → tree(含 type=dir 子目录树,递归)→ note → blob,parents 递归);对象共享,禁止按项目清扫
- 未标记对象删除并计数
- 误删保护:GC 清扫前自动导出分支表为 JSON 备份文件(配置项 KB_GC_PROTECT,默认 on;备份失败则中止 GC);MVP 不做 reflog
- 保护分层:CLI 默认开启;repo 库层默认关闭,由调用方显式开启;备份文件不自动清理,由运维按保留策略归档或删除

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

### 6.7 暂存工作流(stage→commit,借鉴 git)

- **形态**:`note set/rm`、`dir rm` 加 `--stage` 进入暂存;`kb stage` 查看清单;`kb commit [-m] [--abort]` 提交/丢弃。与「每条即提交」的默认语义并存,显式进入
- **落点**:暂存写入独立分支 `<branch>-stage`(快照不建索引)——单条暂存成本恒定(无索引重写);暂存分支 gc 安全(可达)、随 backup 走、不参与默认 pull
- **基线与差异**:进入暂存时落一个「基线快照」(Message=`stage base`,树=当时 main 树,Index 空);commit 计算 **基线↔暂存** 的叶子差异(这就是用户暂存的变更集,删除有显式 tombstone),应用到**当前** main 树 → 单快照 + 一次索引批量增量 → 删除暂存分支。main 在暂存期间的前进保留(同名路径以暂存为准覆盖;无三方合并)
- **边界**:暂存内容在 commit 前对检索/历史不可见;空目录不支持暂存(`dir add --stage` 拒绝);abort 后暂存快照成孤儿由 gc 清理

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
