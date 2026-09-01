# 落地路线图

每个里程碑交付可独立验收的能力;验收标准即测试用例的来源。

> 状态:M1–M3.10 已交付并通过验收(M3.10=存储后端可插拔:SQLite 默认、PostgreSQL 可选);M3.11=dir tree 全库视图(展示层增量)已交付;M4 为可选项,未开工。

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

## M4 检索与集成(可选)

**范围**:倒排索引分片纳入快照、搜索命令或 HTTP API。

**验收标准**
- 同一快照重复搜索,结果与顺序完全一致(可复现)
- 更新一篇笔记后,只有受影响分片地址变化,其余分片结构共享
- (若做 HTTP API)对同一快照的读写经 API 与 CLI 结果一致
