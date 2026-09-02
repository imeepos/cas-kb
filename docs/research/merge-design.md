# cas-kb 三方合并(merge)设计调研(T36)

> 任务:T36 纯文档调研(不含任何代码)· 分支 `research/merge-design`(独立 worktree,不合并不推送)
> 依据:DESIGN.md §2 核心思想、§3 对象模型、§6.2 pull、§6.3 合并(演进项);ROADMAP.md M3/M4;现状实现 `internal/repo/pull.go`、`internal/repo/ancest.go`、`internal/repo/transfer.go`、`internal/repo/stage.go`、`internal/repo/gc.go`、`internal/repo/fsck.go`、`cmd/kb/pull.go`
> 约定:文内地址均为 `sha256:` 前缀 + 截断 hex 的**示意**(真实地址为 64 位小写 hex,见 `internal/hash`);「ours/theirs/base」分别指本地分支头 / 远端分支头 / 最近公共祖先基准

## 0. TL;DR

1. 现状 pull 只有三种结局:已更新(两头相等)、fast-forward(本地 ∈ 远端祖先链)、**分叉即停**(`ErrDiverge`,唯一出路是破坏性的 `--force` 覆盖)。两台机器互写是 AI 灌注场景的常态,分叉即停等于同步能力缺了半边。
2. 本设计在内容寻址库上做**条目级三方合并**:基准取两分支头在快照 DAG 上的最近公共祖先(复用 parents 链,不信任 Time);按全路径对三侧条目地址做三元组判定——单侧变取单侧、双侧同变(同地址)自动合、双侧异变登记冲突;目录级递归下钻,Merkle 地址相等即整棵子树剪枝。
3. **v1 不做文本行级合并**:知识条目短、语义合并价值低、地址即内容天然去重;冲突交人工/AI 裁决。行级合并的正确入口是上层 Agent(把三侧喂给模型),不是 CLI 内置 diff3。
4. 合并结果是**普通快照但含两个 parents**(`Snapshot.Parents` 本就是列表,`omitempty`):对象编码、fsck、GC、pull 传输层零改动兼容;这正是 theirs 侧历史在合并后仍 GC 可达的根因。
5. 冲突采用**全有或全无 + 显式中间态**:冲突时不落提交不动指针,登记 `<branch>-merge` 中间态分支与 meta 键,输出冲突清单;裁决走现成 `--stage` 旗标,`kb merge --continue | --abort` 收束。命令入口 **`kb pull --merge`**(与 `--force` 互斥,默认行为不变)。
6. 结论给出可立项的 ROADMAP 里程碑 **M5(repo 内核 A 批次 + CLI/中间态/e2e B 批次)**,含验收标准草案与验收命令(§5)。

## 1. 动机与现状

### 1.1 现状:pull 的三种结局(代码证据)

| 结局 | 触发条件 | 现状行为 |
|---|---|---|
| 已更新 | 本地头 == 远端头 | 空操作(O(1)),输出「已是最新」 |
| fast-forward | 本地头 ∈ 远端头祖先链(`isAncestor` 沿 parents BFS) | 传输缺失对象后推进分支指针 |
| 分叉拒绝 | 其余一切(含**本地领先**) | `ErrDiverge`;命令层提示「需要 --force 才能覆盖」 |
| 强制覆盖 | `--force` | 无视祖先关系把分支指到远端头,**本地独有提交变不可达,交 GC 清扫** |

出处:`internal/repo/pull.go`(UpToDate 仅当两头相等;fast-forward 判定即 §6.2 的「本地头 ∈ 远端头祖先链」)、`internal/repo/ancest.go`、`cmd/kb/pull.go`。两个值得记录的现状缺口:

- **本地领先也报分叉**:远端头 ∈ 本地祖先链时(本地已包含远端全部内容),现状同样拒绝、要求 `--force`——而此时正确语义是「已是最新」空操作。分叉场景里误用 `--force` 会把自己的新提交整个抹掉。
- **分叉后无收束手段**:除 `--force`(丢一边)外没有任何无损出路。DESIGN §6.3 把三方合并列为演进项,本文件兑现该演进项的设计。

### 1.2 典型分叉场景:两台机器互写

AI 维护的知识库天然多端:笔记本上的 CLI 与内网 102 主机的库(或 `kb serve` 写端点)各自向同一逻辑分支灌注条目。两库互为 `KB_REMOTE_DSN` 远端:

```
机器 A(库 A,分支 main)          机器 B(库 B,分支 main)

  s0 ── s1  (改 workflow/inbox)     s0 ── s1' (改 workflow/daily)
        │                                 │
        A pull B:s1 与 s1' 互不为祖先 → ErrDiverge
        B pull A:同样 ErrDiverge
```

此后每台机器每次 pull 都被拒,只有三条路:

1. `--force` 覆盖对方 → 丢掉一侧的提交(且被丢侧往往还要在另一台机器上重打);
2. 人工比对两库(`kb log` / `kb diff` 各自从 s0 重放)→ 易错、不可审计;
3. 不再同步 → 库的价值(单一天然事实源)瓦解。

**AI 灌注放大器**:`kb bulk import` 把批量写入压成一次提交,两端各灌一批就各产生一个分叉提交;写入频率越高,分叉越常态化。同步能力必须原生含合并,否则「多端」只是口头承诺。

### 1.3 为什么合并是唯一无损收束

- 内容寻址保证**合并不丢历史**:合并提交引用两个 parents,s0→s1 与 s0→s1' 两条链都保持可达;
- 对象层**零新增形态**:合并只产生新 tree / 新 snapshot 对象,note/blob 在各侧提交时已存在,同地址幂等;
- 与 §1.1 的两个缺口一并修复:up-to-date 修正(本地领先空操作)+ 分叉时 `--merge` 收束,行为矩阵闭合。

## 2. 设计:内容寻址库上的三方合并

### 2.1 总览与不变量

合并是**纯树函数 + 一段落库流程**:

```
LCA(ours, theirs) → base
threeWayMerge(base.tree, ours.tree, theirs.tree)
    → { mergedTree(自动合并树), conflicts[](冲突清单) }
conflicts 为空 → 落库:写新 tree 链 → 索引增量 → 快照(parents=[ours, theirs]) → 推进分支
conflicts 非空 → 建中间态(<branch>-merge 分支 + meta 键),输出清单,退出码非零
```

不变量全部沿用 DESIGN §2:

- 对象不可变;可变状态仍收敛于命名空间小表(branches 指针 + 中间态复用分支指针与 meta 键值,不引入第三种可变形态);
- 全程对象幂等:任何一步失败重试安全,最坏留下未达对象交 GC;
- 冲突即停:**不动分支指针、不产生正式提交**(全有或全无);
- 合并提交是普通快照对象,只是 `Parents` 有两个地址——见 §2.6 兼容性论证。

### 2.2 基准选取:LCA(最近公共祖先快照)

**基准 = 同时是 ours 与 theirs 祖先的快照中最「深」者**(被共同祖先集中其他成员可达者被排除)。算法草案(全部复用现成原语):

```
A ← ancestors(ours,含自身)      // 沿 parents BFS,同 isAncestor 的遍历方式
B ← ancestors(theirs,含自身)
C ← A ∩ B                        // 共同祖先集合
M ← { c ∈ C | 不存在 c' ∈ C, c'≠c 且 c 可由 c' 沿 parents 可达 }   // 最深公共祖先
|C| = 0  → 拒绝:「两库无共同历史,无法三方合并」(两库各自 init 即此态)
|M| = 1  → base = M(链式历史恒为此态:v1 之前每快照至多一个 parent)
|M| ≥ 2  → 多 LCA(蟹状历史),见下
```

两条纪律:

- **祖先判定只认 parents 链,不信任 `Time`**:时间戳可回拨、机器时钟不同步,只能做展示;现成 `isAncestor`(`internal/repo/ancest.go`)已是正确语义来源。
- **复杂度量级**:共同祖先集两两可达性校验 O(|C|²) 次 BFS。cas-kb 的快照对象仅几十字节、个人/小团队库快照数千级,v1 直接可接受;实现提示:先按 BFS 首达深度降序过滤(非共同祖先集中最深的必然先出现)可把候选缩到极小,再两两校验。进一步优化列演进项。

**多 LCA(蟹状历史)的两案与取舍**:

| 方案 | 行为 | 优点 | 缺点 |
|---|---|---|---|
| 甲:拒绝 + 显式 `--base <快照>` | 检出 |M|≥2 时响亮失败,列出全部候选,允许 `kb pull --merge --base <短标识>` 显式指定 | 语义清晰;错误基准的锅在人机交界处显式化;实现最小 | 多一步人工判断;自动化场景(定时同步)会卡住 |
| 乙:确定性择一并记录 | 规则选取(候选中 BFS 深度最深,平局取地址字典序最大),合并快照 Message 记录全部候选 | 全自动,不卡定时任务 | 不同基准产生不同合并结果(criss-cross 下可能假冲突甚至假干净);「为什么选了它」要靠读 Message 才能审计 |

**v1 定案:方案甲**。理由:(a) cas-kb 一贯「响亮失败 + 可行动提示」;基准选错的表现极隐蔽——假干净(把不该丢的变更当「双侧同变」吞掉)比报错危险得多;(b) 多 LCA 只会在**合并特性落地之后**由合并自身产生(v1 之前 DAG 是链),初期蟹状历史极少,人工指定成本趋近于零;(c) 方案乙是纯增量——先有 `--base` 入口,自动选取只是把「人指定」换成「规则指定」,随时可加。演进项依次:自动择一 + Message 记录候选 → git 式 recursive 合并(对多个候选先做一轮合并、以其结果为新基准),复杂度高,无真实 workload 证据不启动。

### 2.3 粒度:条目级三方判定 + 目录级递归

**模型**:把三棵树按全路径对齐为逐 slug 的三元组(条目值 = `(type, addr)`,不存在记 ⊥),目录(`type=dir`)递归下钻。判定表:

| # | base | ours | theirs | 判定 | 动作 |
|---|---|---|---|---|---|
| 1 | x | x | x | 未变 | 跳过(地址相等零成本) |
| 2 | b | b | t | theirs 单侧变 | 取 theirs |
| 3 | b | o | b | ours 单侧变 | 取 ours |
| 4 | b | o | o(≠b) | 双侧同变(含双侧同删:o=t=⊥) | 取 o(同字节必同地址,自动合) |
| 5 | b | o | t,o≠t,均非 ⊥ | **内容冲突**(双侧异改;含 add/add:b=⊥) | 登记,树中暂取 ours 占位 |
| 6 | b | ⊥ | t(≠b) 或对称 | **修改/删除冲突**(删改对撞) | 登记,树中暂取 ours 占位 |
| 7 | dir | dir′ | dir″ 三方互异 | 目录双侧异变 | **递归下钻**(§2.3.2) |
| 8 | b | dir/note 对撞 | | **类型冲突**(一侧把条目改成目录或反向) | 登记(树中暂取 ours) |

说明:

- 判定只比较 **(type, addr) 三元组,不读 note/blob 字节**。三侧内容是否相同由地址相等直接回答——这是内容寻址的判定红利,合并成本 = 三棵树的条目表遍历,与正文长度无关。
- 类型冲突(行 8)的例子:ours 把 `go` 从笔记扩成目录(`go/`),theirs 修改了 `go` 笔记本体。按全路径 flatten 的粗粒度模型会把它误报成「删除 + 新增一堆路径」;逐 slug 携带 type 判定才能精确报「`go` 类型冲突」。
- **双侧同变是免费的**:两台机器独立写入逐字节相同的变更 → 同地址 → 行 4 自动合,连传输层都因对象幂等而零冗余。行级合并方案在这里需要 diff3 才能做到的事,地址比较一步完成。

#### 2.3.1 Merkle 剪枝(内容寻址的第二重红利)

tree 地址即整棵子树的指纹:某 slug 下三侧地址两两相等 ⇒ 该子树逐字节相同。因此:

- ours 子树地址 == base ⇒ ours 未动该子树,**整棵取 theirs,零下钻**;
- theirs 子树地址 == base ⇒ 整棵取 ours;
- 三址相等 ⇒ 原样复用。

下钻深度 ∝ 实际分叉路径,与库总量无关;两机互写的典型分叉(各改几条)合并成本是常数级。最坏情况(双侧全库重写)退化为 O(N) 条目表比较,N ≤ 十万条是本系统已声明的工作量级(DESIGN §7)。

#### 2.3.2 目录级递归与合成

双侧目录异变(行 7)递归下钻;子层全部落进行 1–4 → 产出**合成子树**(自底向上 `putTree`,只有变化的层写新 tree,兄弟层地址结构共享——与写路径 copy-on-write 同构);子层出现任何冲突 → 该目录路径登记冲突,自动合并树中**该目录整体取 ours 版本占位**(保证中间态树始终是合法可读树,裁决时以 `--stage` 覆盖)。

### 2.4 为什么 v1 不做文本行级合并

1. **知识条目短,行级合并价值密度低。** 源代码文件成千上万行、多人在同一文件协作,行级合并收益巨大;知识条目平均几百字节到几 KB(一 个 slug 一件事),双侧同时改同一条目的窗口极小,改到需要逐行缝合的概率更低。
2. **语义合并价值低,且坏合并不可见。** 行级 diff3 保证的是「语法上无重叠冲突」,不保证语义。两条 AI 生成的笔记对同一事实各写一版,行级合并可能产出「两段都保留、互相矛盾」或「缝出一句话意相反」的文本——**冲突可见可裁决,坏合并静默污染知识库**,对以「可证明完整、可审计回滚」为卖点的系统是负资产。
3. **地址即内容,天然去重,不需要 diff。** 三方判定里真正需要区分内容的只有「双侧异变」一类;而双侧同变、单侧变、未变三类都由地址比较直接判定。行级合并引入的只是把第 5 类冲突强行变成「自动产出一个新 blob」——收益是少一次人工,代价是 §2.4.2 的静默污染风险。
4. **裁决动作本来就是「重新写这条」。** git 的冲突解决终局也是人重新编辑文件;cas-kb 里这个动作就是 `kb note set`(或 `note rm` 接受删除),配合三侧内容可读(`kb note get <路径> --at <base/ours/theirs 短标识>`、`kb diff <base> <ours>`)成本可控。
5. **语义合并的正确入口是上层 Agent,不是 CLI。** 本系统的目标形态是 AI 维护(AI 消费):把冲突路径的 base/ours/theirs 三侧正文交给模型、让它产出合并稿,再 `note set --stage` 写回——这是设计文档(2026-09 调研,Mem0 四操作等)已验证的「LLM 合并」路线。CLI 内置行级合并反而堵死这条更正确的路:v1 把冲突**显式暴露**为结构化清单,把「怎么合并」留给最适合做语义合并的一层。

结论:v1 冲突即人工(或上层 Agent)裁决。若未来某类条目(如纯流水账)被证明高频冲突且语义合并安全,可在**条目类型或路径规则**上开放可插拔合并器,那是演进项,不动今天的地基。

### 2.5 结果:合并提交、冲突中间态与 --continue / --abort

#### 2.5.1 零冲突:落库流程

1. 自底向上写合成树的新 tree 对象(未变层地址复用);
2. 索引增量:`updateIndex(ours.index, ours.tree, mergedTree)`(现成机制,按新旧树叶子差集);mergedTree == ours.tree 时零差异,**结构共享复用 ours 索引地址,增量成本为零**;
3. 写合并快照:`{root, parents=[ours头, theirs头], time, message}`;
4. `BranchSet` 推进分支。全程幂等,失败重试安全。

**零树差异仍要落合并快照**:若 theirs 相对 base 的全部变更 ours 都已包含(树内容与 ours 相同),跳过提交会导致 theirs 侧提交从新头**不可达**,下次 GC 被清扫——历史丢失。合并提交的价值不在树而在**拓扑**:两个 parents 让两侧历史都保持可达。fast-forward 场景(无需新对象)不在本节讨论,它本来就不产生合并提交。

#### 2.5.2 有冲突:显式中间态

冲突时**不落任何正式提交、不动分支指针**,改为登记合并中间态:

- **`<branch>-merge` 暂存分支**:落一个基线快照(Message 固定 `merge base`,树 = 自动合并树(冲突条目取 ours 占位),parents = [ours 头],`NoIndex` 不建索引)——形态与 `<branch>-stage` 基线快照完全同构(DESIGN §6.8);
- **meta 键** `merge.<project>.<branch>` = JSON `{"base":"<LCA 地址>","theirs":"<theirs 头地址>"}`(meta 是全局键值表,键自带项目与分支命名空间;单键存 JSON 保证「检测存在 = 合并中」与清理的原子性);可变状态仍只有命名空间小表;
- **冲突清单输出**(退出码非零):文本逐行「路径 / 类别 / base / ours / theirs 短标识」,`--json` 输出结构化契约:`{path, kind, base, ours, theirs}` 一行一条——`kind ∈ {content(双侧异改,含 add/add), modify-delete(删改对撞), type(note↔dir 对撞)}`;字段名与结构即契约,变更须在本文档与 ROADMAP 显式记录(§4.6 输出契约惯例)。

**裁决**:对每条冲突路径执行 `kb note set <路径> … --stage`(写入合并稿)或 `kb note rm <路径> --stage`(接受删除)——复用现成 `--stage` 旗标与暂存分支机制(写入 `<branch>-merge` 视图);三侧内容用 `note get --at` / `diff` 对比。大批量裁决(如「全收 theirs」)不在 v1 命令面(§4 风险 3)。

**收束**:

| 命令 | 语义 |
|---|---|
| `kb merge --continue [-m msg]` | 重算「merge base 基线 ↔ -merge 头」差异应用到自动合并树(现成 stage commit 逻辑),索引增量,写快照 **parents=[ours 头, theirs 头]**(theirs 自 meta 键取),推进分支,删 `<branch>-merge` 与 meta 键。ours 头按冻结纪律(下)与建态时一致 |
| `kb merge --abort` | 删除 `<branch>-merge` 分支与 meta 键;中间态快照成孤儿交 GC(与 `kb commit --abort` 同款语义);输出放弃的裁决条数 |
| 无中间态时 `kb merge …` | 响亮失败:「没有进行中的合并」并给出 `kb pull --merge` 指引 |

**冻结纪律(合并中间态期间)**:该分支的一切直接写路径(`note set/rm` 不带 `--stage`、`dir add/rm`、`bulk import`、`import md`、`reset`、serve 写端点)一律响亮拒绝,提示先 `--continue`/`--abort`;普通 `kb stage`/`kb commit` 同拒(防止把合并裁决误当普通暂存)。理由:若 ours 头在合并期间前进,continue 重放的差异会把新提交静默回退(基线树落后)。冻结把这一类竞态在语义层关死。

**设计取舍记录**:备选方案是复用 `kb commit`/`kb commit --abort` 承担收束(命令面零新增),但 `kb commit` 将出现「普通提交 / 合并提交」双态歧义,误提交半成品合并的代价高;独立子命令显式、可校验,符合「响亮失败」惯例。§4 风险 4 记录与 stage 的互斥规则。

#### 2.5.3 两 parents 合并提交的兼容性论证(零 schema 变更)

`object.Snapshot.Parents` 本就是 `[]Address` + `omitempty`(DESIGN §3)——**对象编码不变,schema v5 不动**,两 parents 快照与既有解码器逐字节兼容。逐机制核对:

| 机制 | 现状行为 | 多 parents 影响 |
|---|---|---|
| `childrenOf(snapshot)` | kids = root + **全部** parents + index | 已兼容:transfer(pull 取对象)与 GC 标记顺第二 parents 下钻 |
| fsck `checkSnapshot` | 校验 root + **全部** parents 存在 | 已兼容,零改动;验收断言合并后 fsck 零问题 |
| GC `snapshotDepths` | 从各分支头 BFS 取**最浅深度**(跨链共享祖先取最小) | 已兼容;合并会以「经合并点的距离」重算另一条链的深度,水位语义仍自洽(§4 风险 6) |
| `reachableSnapshots`(短标识解析) | 从全部分支头沿**全部** parents | 已兼容:合并提交落库后 theirs 侧历史快照进入可达集,可被 `--at` 与短标识引用——正是期望行为 |
| `Log` | **只沿 `Parents[0]`** | **不兼容(展示面)**:第二 parents 链不展示。v1 接受 first-parent 展示(合并提交本身可见、Message 注明 `merge`),`--all`/图形态列演进项(§4 风险 7) |
| backup/restore | 对象级全量 JSONL,快照无特殊化 | 兼容;restore 后照例 fsck 复核 |

### 2.6 与 gc / fsck / backup / 索引 / reset 的交互汇总

- **GC**:合并快照是普通可达对象,两 parents 历史均被标记——「必须两 parents」的根因即在此;中间态 `<branch>-merge` 是普通分支,`BranchListAll` 覆盖,合并进行中跑 GC 安全(中间态树/裁决内容可达);`--abort` 后孤儿按既有语义清理;GCProtect 分支表备份自动包含中间态分支(多一层误删保护)。验收需钉死:合并 → `gc` → `fsck` 零问题,ours/theirs 两侧提交均未被清扫。
- **fsck**:零改动(§2.5.3);验收断言「合并后 fsck 通过」与「篡改合并快照字节报错」(完整性检查天然覆盖新对象)。
- **backup/restore**:`-merge` 中间态随分支行走(备份含中间态分支与 meta?——**meta 键不在 backup 载荷里**,restore 后合并中间态丢失,仅剩孤儿分支。v1 口径:backup 前提是干净工作区(与现约定一致),文档明示「合并进行中不保证可备份恢复中间态」;改进列开放问题 §4-8);
- **检索索引**:正式合并提交必须建索引(与一切正式提交同纪律);增量正确性验收:`merge --continue` 后 `search` 结果与 `index rebuild` 后全量一致(§5 B 批次);
- **reset**:合并后照常回退;放弃合并提交则两侧历史成不可达交 GC(与 §6.7 语义一致);
- **gc --keep-last**:深度按最浅计算,合并点两侧链的水位边界随之重算;验收用例:合并历史下 `gc --keep-last K` 不误清合并快照本体及其索引。

### 2.7 与 pull 的集成:`kb pull --merge`

- **入口**:`kb pull [remoteDsn] [--force | --merge]`。`--force` 与 `--merge` 互斥(同时给出即报错);不带旗标时行为与现状逐字节一致(分叉拒绝)——向后兼容,给用户渐进迁移路径,分叉提示文案追加「可执行 `kb pull --merge` 做三方合并」。
- **判定矩阵闭合**(顺带修正 §1.1 缺口):

| 祖先关系 | 无旗标 | `--force` | `--merge` |
|---|---|---|---|
| 本地 == 远端 | 已更新 | 已更新 | 已更新 |
| 本地 ∈ ancestors(远端) | fast-forward | fast-forward | fast-forward |
| 远端 ∈ ancestors(本地) | 分叉拒绝(**修正:改为已更新空操作**) | 覆盖(回退) | 已更新空操作 |
| 互不为祖先 | 分叉拒绝 | 覆盖 | **三方合并** |
| 无共同祖先 | 分叉拒绝 | 覆盖 | 拒绝:「无共同历史」 |

- 「本地领先」从「分叉拒绝」改为「已更新」是对现状误报的纯修正(与 git 语义对齐),不依赖 `--merge`,随本批次一并交付并单独验收;
- **远端分支与本地分支不同名**:`pull` 现以同名分支(`KB_BRANCH`)对拉,v1 不引入 refspec 映射,维持现状;
- **独立 `kb merge <引用>`**(接受任意本地快照引用、同库跨项目合并)列演进项:入口命令先收敛在一个场景(两机互写),UX 验证后再扩。

**完整命令生命周期**:`kb pull --merge` 发起 →(零冲突)直接完成 / (冲突)建中间态 → `note set/rm --stage` 裁决(可多次,进度查 `kb merge --continue` 前的清单重打;`kb stage` 在合并中态改为展示合并裁决清单)→ `kb merge --continue` 收束 / `kb merge --abort` 放弃。

## 3. 关键例子:一次完整的三方合并演算

场景:机器 A(ours)与机器 B(theirs)从同一快照 `s_base = sha256:s00a…` 出发各自提交一次后互拉。知识库内容刻意覆盖全部判定类别。

### 3.1 三棵具体的树

**base(共同祖先,快照 `s00a…`,parents=[])**——root tree `sha256:000a…`:

| slug | type | addr | 内容(标题) |
|---|---|---|---|
| `owner` | note | `sha256:aa50…` | 库主人 |
| `workflow/` | dir | `sha256:001a…` | (子树见下) |
| `go` | note | `sha256:aa30…` | Go 并发备忘 |

子树 `workflow/`(`sha256:001a…`):

| slug | type | addr | 内容 |
|---|---|---|---|
| `inbox` | note | `sha256:aa10…`(body blob `b010…`) | 收件箱 |
| `daily` | note | `sha256:aa20…` | 每日站会纪要 |

**ours(机器 A,快照 `s00b…`,parents=[s00a])**——root tree `sha256:000b…`:

| slug | type | addr | 变更 |
|---|---|---|---|
| `owner` | note | `sha256:aa50…` | 未动 |
| `workflow/` | dir | `sha256:001b…` | 子树变了(见下) |
| `go` | note | `sha256:aa3f…` | **改**(补一行「channel 关闭后 range 退出」) |
| `kb` | note | `sha256:aa40…`(blob `b040…`) | **新增** |

子树 `workflow/`(`sha256:001b…`):`inbox` → `sha256:aa1f…`(blob `b01f…`,**改**:加一行「周三评审」);`daily` → `aa20…` 未动。

**theirs(机器 B,快照 `s00c…`,parents=[s00a])**——root tree `sha256:000c…`:

| slug | type | addr | 变更 |
|---|---|---|---|
| `owner` | note | `sha256:aa50…` | 未动 |
| `workflow/` | dir | `sha256:001c…` | 子树变了(见下) |
| `go` | note | `sha256:aa3f…` | **改,且与 ours 逐字节相同**(同地址!) |

子树 `workflow/`(`sha256:001c…`):`inbox` → `sha256:aa1e…`(blob `b01e…`,**改成了不同内容**:加「周四评审」);`daily` → ⊥(**删除**)。

### 3.2 逐步演算

**第 1 步:LCA**。ancestors(ours) = {s00b, s00a},ancestors(theirs) = {s00c, s00a},交集 {s00a},最深公共祖先唯一 → **base = s00a**。

**第 2 步:根层逐 slug 三元组**(值 = (type, addr)):

| slug | base | ours | theirs | 判定(对照 §2.3 表) | 动作 |
|---|---|---|---|---|---|
| `owner` | aa50 | aa50 | aa50 | 行 1 未变 | 跳过(零成本,Merkle 剪枝同类) |
| `workflow/` | 001a | 001b | 001c | 行 7 目录双侧异变 | **递归下钻** |
| `go` | aa30 | aa3f | aa3f | 行 4 双侧同变(ours==theirs≠base) | 取 `aa3f`,**不读对象、不产生冲突** |
| `kb` | ⊥ | aa40 | ⊥ | 行 3 ours 单侧变 | 取 `aa40` |

**第 3 步:下钻 `workflow/`**:

| slug | base | ours | theirs | 判定 | 动作 |
|---|---|---|---|---|---|
| `inbox` | aa10 | aa1f | aa1e | 行 5 **内容冲突**(双侧异改:周三 vs 周四) | 登记;树中暂取 ours(`aa1f`)占位 |
| `daily` | aa20 | aa20 | ⊥ | 行 2 theirs 单侧删 | 取删除 |

**第 4 步:合成自动合并树**(自底向上只写变化的层):

- `workflow/` 新 tree `sha256:001d…` = { inbox → aa1f(占位),daily → ⊥ };
- root 新 tree `sha256:000d…` = { owner → aa50, workflow/ → 001d, go → aa3f, kb → aa40 }。

**第 5 步:冲突汇总与输出**。自动合并 3 条(`go` 同变、`kb` 单侧增、`workflow/daily` 单侧删),冲突 1 条,`kb pull --merge` 退出码非零并输出:

```
$ kb pull postgres://…  --merge
传输 6 个对象;分叉:base s00a… ours s00b… theirs s00c…
自动合并 3 条;冲突 1 条:
  workflow/inbox  content  base aa10…  ours aa1f…  theirs aa1e…
合并未完成:逐条 kb note set/rm <路径> --stage 裁决后 kb merge --continue;或 kb merge --abort 放弃
```

同时落中间态:`main-merge` 分支(基线快照:root=`000d…`,parents=[s00b],Message `merge base`,无索引)+ meta 键 `merge.default.main` = `{"base":"s00a…","theirs":"s00c…"}`。

**类别覆盖小结**:未变(owner)、单侧改(kb)、单侧删(daily)、双侧同变(go)、双侧异改=冲突(inbox)、目录递归(workflow/)——六个判定类别一网打尽;修改/删除冲突与类型冲突由判定表其余行覆盖(§5 A 批次单测逐类断言)。

### 3.3 裁决与合并提交

人(或上层 Agent)读三侧:`kb note get workflow/inbox`(=ours 占位)、`kb note get workflow/inbox --at aa1e…` 所在快照(用 theirs 短标识)、`kb diff s00a… s00c…`。设裁决为「以 theirs 为底手改评审时间」:

```
$ kb note set workflow/inbox --title 收件箱 --tags review --stage -m "采用 theirs 版本,评审定于周三并知会 B"
已暂存 workflow/inbox(aa1d…)
$ kb merge --continue -m "merge theirs:收件箱裁决、daily 删除、go 同步"
合并完成:1 条裁决,0 条遗留;快照 s00d…
```

`--continue` 内部:重算「merge base ↔ -merge 头」差异 = {inbox: aa1f→aa1d},应用到自动合并树 → 最终树:

- `workflow/` 新 tree `sha256:001e…` = { inbox → `sha256:aa1d…`(blob `b01d…`) };
- root 新 tree `sha256:000e…` = { owner → aa50, workflow/ → 001e, go → aa3f, kb → aa40 };
- 索引增量(ours 索引 → 新 indexroot `i00d…`,仅 inbox 一个文档的词频变化);
- 合并快照 `sha256:s00d…`:`{root:"000e…", parents:["s00b…","s00c…"], time:…, message:"merge theirs:…", index:"i00d…"}`;
- `BranchSet(main, s00d…)`,删 `main-merge` 与 meta 键。

事后自检(全部应为既有行为):`kb log` 显示合并提交(first-parent 链 s00d→s00b→s00a,Message 注明 merge);`kb note get workflow/daily --at s00c…` 仍可读(theirs 链可达,短标识解析可达集已含 s00c);`kb fsck` 零问题;`kb gc` 后两侧历史完整;`kb search 周三` 命中新 inbox。

### 3.4 对照:无冲突时的一步合并

若 B 没改 `inbox`(theirs 的 workflow 子树里 inbox 仍为 aa10):第 3 步 inbox 落行 2 单侧变(仅 daily 删除),零冲突 → `kb pull --merge` 一次完成:传输对象 → 写 `001d′/000d′` 新树 → 索引增量 → 快照 parents=[s00b, s00c] → 推进指针,退出码 0。若 B 什么都没改而 A 全改了(`theirs ∈ ancestors(ours)`),按 §2.7 矩阵直接「已是最新」,连合并都不进。

## 4. 风险与开放问题

1. **LCA 开销与「无共同祖先」**。祖先闭包 BFS 需加载沿途全部快照对象;共同祖先集大时两两校验 O(|C|²)。量级上(快照数千、对象几十字节)v1 可接受,但必须把 **|C|=0(两库独立 init)显式拒绝**做进验收——这是跨库合并最常见的硬失败,报错要可行动(指引 `--force` 或确认两端同源)。开放:大 DAG 的增量祖先缓存。
2. **多 LCA 策略与确定性**。v1 拒绝 + `--base`(§2.2),但蟹状历史一旦常见,人工指定会成瓶颈;届时上「确定性择一 + Message 记录」仍可能在不同机器上因候选集不同而选择不同基准(可达集裁剪后),产生分叉库间的再分叉。开放问题:是否引入 recursive 合并;如何对「合并结果的确定性」定义验收(同库同输入重跑必须同地址,跨库不承诺)。
3. **冲突 UX 与批量裁决**。千条冲突时逐条 `note set --stage` 不可操作。v1 不做 `--favor ours|theirs` 批量策略(容易被滥用成变相 `--force`),但 AI 裁决路径(上层 Agent 拉清单 → 批量产出合并稿 → 批量 `--stage`)的可操作性依赖清单契约稳定与 `--at` 三侧可读。开放:是否提供「按路径前缀批量采纳」白名单机制。
4. **与 `--stage` 暂存分支的交互**。同分支 `-stage` 与 `-merge` 并存时语义打架:v1 以冻结纪律关死(合并中态拒绝普通 stage/commit 与一切直接写);`kb stage` 在合并中态切换为展示裁决清单。遗留:用户手工创建名为 `<branch>-merge` 的普通分支会被误认中间态——CLI 建分支面目前不存在(分支由机制隐式创建),风险低,但要在 fsck/文档标注保留名。
5. **跨项目语义**。同库跨项目 pull 现为零传输指针推进;若放开跨项目合并,合并快照落在目标项目、第二 parents 历史跨项目可达,短标识解析可达集将跨项目膨胀(与 §4.5「项目内解析」冲突),且两项目同路径异语义的冲突更难裁决。v1 限定合并仅限同项目;跨项目合并列开放问题(需要先回答「项目间知识搬运」的产品语义)。
6. **gc --keep-last 水位与合并拓扑**。深度按 BFS 最浅计算,合并后另一条链的深度以「经合并点」重计,历史索引精简的边界会移动(相对合并前直觉);可能提前精简 theirs 链上较旧快照的索引(`search --at` 报友好错误,数据本体不受影响)。验收须覆盖「合并 → gc --keep-last K → 合并快照本体与索引保留」;开放:是否为合并快照豁免水位。
7. **`kb log` 展示面**。first-parent 遍历不展示第二 parents 链,v1 靠合并 Message 注明;但「合并后 log 少了一段历史」的可感知缺失需要文档明示。开放:`log --all`(按可达集全展示,按 time 排序)或图形态输出;与短标识解析可达集(已含两链)的不对称要在文档讲清。
8. **中间态与 backup/serve 的边界**。合并进行中做 backup:分支载荷含 `-merge` 分支但 meta 键不在备份格式里,restore 后中间态不可续(只剩孤儿分支);serve 写端点冻结纪律需要 server 层感知中间态。v1 口径「合并进行中不保证备份中间态、serve 拒绝写」要在文档与命令输出明示;开放:meta 键纳入 backup header 扩展。
9. **API 暴露面**。合并要不要进 HTTP API(如 `POST /api/v1/merge`)?v1 不暴露(§8.6 先例:写 API 只覆盖 note 读写,范围控制);但 AI 裁决走 CLI 还是 API 的通路差异会在多机自动化场景放大(§4-3)。开放:最小只读暴露(`GET /api/v1/merge/status`)还是完全 CLI-only。

## 5. 结论:ROADMAP 里程碑建议(供负责人评审后立项)

**建议里程碑名:M5 三方合并(pull --merge)**,分两批次交付,批次 A 为批次 B 的前置。以下为范围与验收标准草案,评审通过后落入 ROADMAP(并同步 DESIGN §6.2/§6.3 从演进项转正式、usage、CHANGELOG 四处)。

### 批次 A:repo 内核(纯库层,无 CLI)

**范围**:LCA 计算(唯一基准;无共同祖先拒绝;多候选拒绝并列出候选 + `Base` 显式覆盖参数)、三方树合并纯函数(`threeWayMerge(baseTree, oursTree, theirsTree) → {mergedTree, conflicts[]}`,判定表 §2.3 全类别、Merkle 剪枝、目录递归)、冲突结构(`{path, kind, base, ours, theirs}`)。

**验收标准草案**

- 判定表逐类单测:未变 / 单侧改 / 单侧增 / 单侧删 / 双侧同变(含双侧同删)/ 双侧异改(冲突)/ add/add(冲突)/ 修改-删除(冲突)/ 类型对撞(冲突)/ 目录递归合成(子层零冲突自动合成、子层冲突整目录取 ours 占位)
- 确定性:同一三树输入重复合并,合并树地址逐字节一致;注入不同时间源结果不变
- LCA:链式历史唯一基准;两库无共同祖先拒绝且文案可行动;构造蟹状历史(两合并提交并存)检出多候选并列出全部候选;显式指定其中之一可继续
- Merkle 剪枝:双侧未分叉子树零下钻(以读取计数断言)
- 纯函数性:合并计算不推进分支指针、不写索引、冲突时不产生正式提交

**验收命令草案**:`go test ./internal/repo/ -run Merge -v`

### 批次 B:CLI、中间态与端到端

**范围**:`kb pull --merge`(判定矩阵 §2.7 全表,含「本地领先 → 已更新」修正)、`<branch>-merge` 中间态 + meta 键、`kb merge --continue [-m]` / `kb merge --abort`、冲突清单输出(文本 + `--json` 契约)、冻结纪律、`kb stage` 合并中态展示、usage/文档同步。

**验收标准草案**

- e2e 全流程(两库互写):分叉 → `pull --merge` 冲突退出(清单逐字段、退出码非零、无提交落库)→ `--stage` 裁决 → `merge --continue` → 合并快照两 parents 逐地址断言 → `fsck` 零问题 → `search` 命中裁决稿且与 `index rebuild` 后一致 → `log` 可见合并提交
- 零冲突路径:`pull --merge` 一步完成(退出码 0、两 parents 快照、fsck 通过)
- 矩阵修正:本地领先时无旗标 pull 为「已更新」空操作(现状误报回归用例)
- `merge --abort`:`-merge` 分支与 meta 键清除、孤儿交 gc(`gc` 后对象计数下降)、fsck 通过
- 冻结纪律:合并中态下直接写路径 / 普通 stage / 普通 commit / serve 写端点全部拒绝且文案可行动
- gc 交互:合并 → `gc` → 两侧历史与合并快照均保留、fsck 通过;`gc --keep-last K` 下合并快照本体与索引保留
- 无共同祖先:两独立库 `pull --merge` 响亮拒绝
- 兼容性:`pull` 无旗标行为与现状一致(分叉拒绝文案追加 --merge 指引);`--force` 与 `--merge` 互斥
- 文档四处同步:DESIGN §6.2/§6.3、ROADMAP 本节、usage、CHANGELOG

**验收命令草案**:`go test ./cmd/kb/ -run Merge -v`、`go test ./internal/repo/ -run Merge -v`、`./scripts/e2e.sh`(新增 merge 段)

### 明确非目标(v1)

文本行级合并;跨项目合并;独立 `kb merge <引用>` 入口;recursive/自动多 LCA 策略;`--favor` 批量策略;合并的 HTTP API 暴露;`kb log` 图形态。

---

> 本文件为 T36 唯一交付物(docs/research/merge-design.md),不含任何代码改动;评审后由负责人决定是否按 §5 立项。
