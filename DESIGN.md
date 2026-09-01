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
              Tree = H(entries: slug → note 地址)          ← 目录也是内容寻址
                │   │
             Note  Note = H(meta + body地址 + links)        ← 条目节点
              │
            Blob(正文原始字节)                               ← 叶子
```

- **内容寻址**:`addr = "sha256:" + hex(sha256(规范字节))`;算法前缀为未来升级 BLAKE3 留门
- **Merkle 结构**:父节点地址由子节点地址决定,任何叶子变动都会向上传播到根
- **唯一可变点**:整个系统只有 `branches` 表可变((项目, 名字) → 快照地址)。备份、同步、并发全部归结为管理这张小表

## 3. 对象模型

四类对象,地址都是内容哈希:

| 对象 | 载荷 | 地址含义 |
|---|---|---|
| `blob` | 正文原始字节(无信封) | 内容指纹 |
| `note` | meta + body(blob 地址)+ links | 一条知识条目 |
| `tree` | entries: slug → note 地址 | 目录/解析表 |
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

### 3.3 链接解析(版本自洽)

笔记间链接只存人类可读的 **slug**,解析规则:**在当前快照的 root tree 中查 slug → 地址**。

性质:任何历史快照内,链接指向该版本的对象;切换版本,解析随之一致(时间旅行一致性)。改了笔记 A,生成 A′(新地址)+ 新 tree,旧快照里链接依然解析到旧 A——双向链接永不悬空。

MVP 的 tree 是扁平一层(slug → note);嵌套路径目录为演进项。

## 4. 存储设计(PostgreSQL)

### 4.1 表结构

四张表(完整 DDL 见 [schema.sql](schema.sql)):

- **objects** — `addr(text PK), kind, size, data(bytea)`;只增不删(仅 GC 清扫);全局共享、跨项目去重
- **projects** — `name(text PK), created_at`;项目隔离的一等实体(schema v2 新增)
- **branches** — `(project, name)(复合主键), addr(→ objects), updated_at`;**全库唯一可变表**,按项目划分命名空间
- **meta** — `schema_version` 等键值;加载时版本不符直接拒绝(误配置报错要响)

> 版本约定:库 schema 版本(meta.schema_version,当前 2)与对象编码版本(object.SchemaVersion)相互独立;本次 v2 仅动表结构,对象编码与地址不变。

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
- **默认项目**:default;v1 存量库打开时自动迁移,存量分支全部落入 default,旧用法行为不变
- **项目内解析**:短标识解析限定在当前项目分支头的可达集内,不串项目
- **GC 全局**:对象共享时按项目清扫会误删他项目对象,GC 始终从全部项目的分支头出发标记
- **跨项目搬运**:同库内跨项目 pull 只推进分支指针、零对象传输,充当知识共享通道

## 5. Go 工程结构(M1–M3 已按此结构实现)

```
cas-kb/
├── cmd/kb/          CLI 入口:init / note / log / diff / pull / gc / fsck
├── internal/
│   ├── hash/        地址类型、sha256 封装、格式校验
│   ├── object/      四类对象定义、规范编解码、结构校验
│   ├── store/       存储接口 + postgres 实现 + 迁移
│   └── repo/        业务层:提交、解析、日志、diff、pull、gc、fsck
└── schema.sql       DDL 规格(迁移的权威来源)
```

依赖方向:`cmd → repo → store/object → hash`;store 不依赖 repo。

### 5.1 接口契约(文字规格)

**store.Store**(内容寻址存储 + 分支指针):

| 方法 | 契约 |
|---|---|
| Put(kind, data) → addr | 幂等;同地址重复写等价于空操作 |
| Get(addr) → (data, kind) | 不存在返回哨兵错误 NotFound;kind 必须合法 |
| Has / Delete / List | Has 供遍历跳过;Delete 仅 GC 使用;List 供 GC/FSCK 全量扫描 |
| BranchGet / BranchSet / BranchDelete / BranchList | 均按项目作用域(repo 层注入项目);BranchSet 是唯一写路径,UPSERT;目标对象不存在时报错(FK 兜底) |
| Close | 释放连接 |

**repo.Repo**(业务层):PutNote / SetNote / RemoveNote / Note / ListNotes / Commit / Log / Diff / Pull / GC / FSCK。

## 6. 关键流程

### 6.1 写入一条笔记(读-改-写)

```
读分支头(可能不存在)
  → 加载 root tree 的 entries
  → 写正文 blob → 写 note 对象 → entries[slug] = note 地址
  → 写新 tree → 写新 snapshot(parents = [旧头])
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

- 从**所有项目**的全部分支头出发做可达性标记(snapshot → tree → note → blob,parents 递归);对象共享,禁止按项目清扫
- 未标记对象删除并计数
- 误删保护:GC 清扫前自动导出分支表为 JSON 备份文件(配置项 KB_GC_PROTECT,默认 on;备份失败则中止 GC);MVP 不做 reflog
- 保护分层:CLI 默认开启;repo 库层默认关闭,由调用方显式开启;备份文件不自动清理,由运维按保留策略归档或删除

### 6.5 FSCK

- 全表逐对象重算哈希,与地址比对;按 kind 解码,校验内部引用(body/root/entries/parents 均存在)
- 输出问题清单,发现问题时退出码非零——可直接接 CI 巡检

### 6.6 引用解析

- 命令引用(base/tip 等)按三级解析:精确分支名 → 完整地址(原样采用,存在性在读取时校验)→ 快照地址短标识前缀
- 短标识对全部快照做前缀扫描,命中第二个即提前终止并报歧义,唯一命中才生效
- 规模边界:前缀扫描随对象总数线性增长,超大库建议直接使用完整地址

## 7. 检索(M4,可选)

- 倒排索引分片:每分片 = {词 → note 地址列表} 的小 tree,分片地址 = 分片内容哈希
- 索引版本纳入快照(格式演进时给 snapshot 加 index 字段并升 schema_version)→ **同一快照搜索结果必然一致(可复现、可审计)**
- 更新只重建受影响分片,其余分片地址复用(结构共享);语义向量检索同法处理

## 8. 部署与配置

### 8.1 拓扑

- 生产:PostgreSQL 16 Docker 部署于主机 `102`,库名 `caskb`;Go CLI/服务端同内网访问
- 开发:本机 `docker compose` 起同构实例(compose 片段见 README 附录)

### 8.2 配置项

| 变量 | 默认 | 说明 |
|---|---|---|
| KB_DSN | `postgres://postgres:postgres@127.0.0.1:5432/caskb?sslmode=disable` | 连接串;指向 102 时替换主机部分 |
| KB_BRANCH | `main` | 默认分支 |
| KB_GC_PROTECT | `on` | GC 清扫前自动导出分支表备份;设为 off/0/false 关闭 |
| KB_REMOTE_DSN | (无) | `kb pull` 的远端连接串(也可作为命令行参数传入) |
| KB_TEST_DSN | (无) | 集成测试基库连接串;未设置时跳过集成测试(仅测试使用) |
| KB_PROJECT | `default` | 项目作用域(M3.5):note/log/diff/gc/fsck 等命令只作用于该项目;亦可用 -p 按命令覆盖 |

指向 102 的示例:`postgres://caskb_app:<密码>@192.168.x.102:5432/caskb?sslmode=disable`。
安全要求:专用账号 `caskb_app`(只授 caskb 库权限)、密码走 scram-sha-256、内网传输是否启用 TLS 按内网策略定;**凭据一律走环境变量,不入库不入仓**。

### 8.3 备份与恢复

- 逻辑备份:pg_dump(含 objects/branches/meta)即可完整恢复
- branches 表极小可高频快照;objects 只增,增量备份友好
- 恢复后先跑 fsck 再提供服务

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
| slug | 人类可读的条目名,链接解析的键 |
| 项目 project | 知识库的命名空间单位;分支与版本按项目隔离,对象跨项目共享去重 |
| fast-forward | 本地头是远端头的祖先时,直接推进分支无需合并 |
| 3-way merge | 基于共同祖先的三方合并 |
