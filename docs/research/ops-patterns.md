# cas-kb 运维模式调研:停滞检测与心跳纪律 / 合并双亲结构化暴露 / 回归调度惯例(T57)

> 任务:T57 纯文档调研(不含任何代码)· 分支 research/ops-patterns · 交付物 = 本文件(唯一新增,不修改任何代码与既有文档)
> 检索日期:2026-09-02;全部外链为本次实抓(curl 或等价 HTTP GET),「检索不到/未采信」的项在 §5 如实标注,不做编造
> 背景输入:docs/review/drill-multi-cli.md 与 docs/review/drill-serve.md(演练现场)、DESIGN §8.5(API 惯例)、
> docs/research/best-practices-adoption.md §1.2(GitHub/Gitea parents 惯例,本文引用不重复检索)、
> scripts/drill-multi.sh 与 scripts/drill-serve.sh(已固化演练脚本)
> 每问结构:**社区做法 → 真实证据 → 对 cas-kb 的裁剪建议 → 落地清单草案**;§4 汇总为实施清单表供负责人直接立项

## 0. TL;DR

1. **心跳纪律(建议采纳:三条阈值 + 复活三查,纪律成文 + 一个小脚本)**:commit 即心跳(AGENTS.md「写完立刻存档」的节奏化),心跳间隔 ≤30 分钟、死亡阈值 90 分钟、会话墙钟上限 6 小时;负责人死亡判定脚本化(git for-each-ref 算分支最近 commit 年龄);**复活 ≠ 续写**——复活前必须走「查已交付 / 查分支关系 / 查撞车」三查,复用优先于重写。T54 复活补交撞车报废的根因是三个缺失的叠加(§1.3 写透),社区同构物齐备:GitHub Actions timeout-minutes(外部墙钟)、systemd WatchdogSec+sd_notify(工作者自报心跳)、Slurm HealthCheck/TIMEOUT 终态(外部体检 + 可审计死亡)、borg 僵尸锁「显式 break-lock 才动」。
2. **合并双亲(结论:HTTP 已有,契约不动)**:GET /api/v1/log 的行契约**已经**带 parents 数组(M5 起的 view.LogRow,DESIGN §8.5 端点表已列)——「是否增 parents 数组」这个问题已经发生过,答案是「已增且向后兼容」;CLI kb log 文本的 parent=p1,p2 逗号分隔被 drill 断言依赖,**保持不动**;git %P(空格分隔全哈希,本仓合并提交实测)与 jj .parents() 模板(官方示例 parents.map(|c| c.commit_id().short()))佐证社区共识「拓扑事实以结构化列表原样暴露,分隔符只是展示层选择」。增量只有:DESIGN 注记「parents 与 merge-state 的 ours/theirs 可交叉验证」+ drill 补 ours 对称断言 + 可选 kb log --json。
3. **回归调度(建议采纳:聚合脚本 + launchd 季度任务 + 发版前 checklist,不建常驻 CI)**:scripts/regression.sh 聚合 verify + drill×2(学 Makefile phony 聚合 + verify.sh 既有形态);launchd StartCalendarInterval 季度任务(1/4/7/10 月 1 日,ProgramArguments 数组式 + 交付前 plutil -lint,制度化吸收 T43 D1 教训);cron 季度表达式 0 2 1 1,4,7,10 * 为 Linux 备选;GitHub Actions on.schedule 为可选远备(注意「只在默认分支运行 / 60 天无活动自动停用 / 不支持 @yearly 简写」);产物归档沿既成惯例分两类——报告入 docs/review/(结构化 + VERDICT)、日志带时间戳不入 git。

---

## 1. 调研项 1:长时后台工作者的停滞检测与心跳纪律

### 1.1 cas-kb 现状(含 T54 撞车现场)

- 工作流现状:headless 会话(Txx 编号)在独立 worktree + 分支上交付,AGENTS.md 已有三条铁律(「写完的东西要立刻存档(commit)」「禁止直接在主分支上修改任何代码」「使用 worktree 避免冲突」);交付后由负责人合并,质量门禁 ./scripts/verify.sh。分支名即会话归属(research/*、drill/*、feat/*)。
- 缺口:**没有任何「会话还活着吗」的判定面**。分支最近 commit 年龄无人看、心跳文件不存在、死亡阈值与复活协议均无成文——「死亡」判定与「复活」处置全凭人工记忆,T54 事故正是这个缺口的现场化。
- **T54 撞车事件还原**(用户提供背景;仓库内无独立事故记录文件,本节即为其书面化,如实注明):T54 原会话(M6-A 向量对象模型)长时间停滞,期间无 commit 产出;负责人判定其死亡;此后该会话「复活」,按原计划**补交了一套完整的向量对象模型实现**;而 M6-A 的正式交付已由另一路径完成并合并(main 上 c20982d/33928fb/82c05f3/a3e1ce2 一串,ROADMAP「M6-A 向量对象模型与嵌入重建(T54,已交付)」,CHANGELOG v0.8.0 定版)。复活补交的实现与已合并实现完全重复,最终**整批报废**——浪费的不是一次 commit,而是一整套实现(代码 + 测试 + 文档同步)。

### 1.2 社区通行做法与证据

| 系统 | 机制 | 证据(2026-09-02 实抓) |
|---|---|---|
| **GitHub Actions**(timeout-minutes) | **外部强制的墙钟上限**:job 级「The maximum number of minutes to let a job run before GitHub automatically cancels it. **Default: 360**」——超时由平台自动取消,不依赖工作者自觉;step 级同键,「Maximum: 360 for both GitHub-hosted and self-hosted runners」「must be a positive integer」 | 实抓 github/docs 仓库 raw markdown(content/actions/reference/workflows-and-actions/workflow-syntax.md,timeout-minutes 的 job 级与 step 级两节);渲染页:https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idtimeout-minutes |
| **systemd**(WatchdogSec + sd_notify) | **工作者自报心跳**:服务须周期性发 WATCHDOG=1(sd_notify 手册原文:「ping that services need to issue in regular intervals if WatchdogSec= is enabled」),缺席即按挂起处置(杀进程,按 Restart= 重启);另有 RuntimeMaxSec= 纯墙钟上限(Type=oneshot 不适用——**一次性任务不配运行时上限,正中批处理要害**);EXTEND_TIMEOUT_USEC= 允许在限期内**显式申请展期**,展期后须持续续报 | https://www.freedesktop.org/software/systemd/man/latest/sd_notify.html (WATCHDOG=1/WATCHDOG_USEC=/EXTEND_TIMEOUT_USEC= 各条)与 https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html (WatchdogSec=/RuntimeMaxSec=/TimeoutAbortSec= 联动) |
| **Slurm**(HPC 批处理) | **外部定期体检 + 终态可审计**:HealthCheckProgram(root 定期在计算节点执行的健康脚本,「may be used to verify the node is fully operational and **DRAIN** the node」)+ HealthCheckInterval(**默认 0 = 禁用**)+ HealthCheckTimeout(默认 60s,「should be less than HealthCheckInterval」)+ HealthCheckNodeState(默认 ANY,CYCLE 轮转);作业触顶时间限额进入 TIMEOUT 终态(sacct 手册:「**TO TIMEOUT — Job terminated upon reaching its time limit**」)——死亡是记录在案的状态,不是悬案 | https://slurm.schedmd.com/slurm.conf.html (HealthCheck* 四参数)与 https://slurm.schedmd.com/sacct.html (作业状态表 TIMEOUT 行) |
| **borgbackup**(锁纪律) | **僵尸锁分级处置**:同主机进程死亡后锁可自动回收;**跨主机僵尸锁绝不自动清**(with-lock 手册原文:「Borg is cautious and **does not automatically remove stale locks made by a different host**」),必须 borg break-lock 显式破锁;BORG_HOST_ID 文档从反面证实同主机自动回收的存在(all-zero MAC 会「kill **automatic stale lock removal**」) | https://borgbackup.readthedocs.io/en/stable/usage/lock.html (with-lock/break-lock 两节)与 https://borgbackup.readthedocs.io/en/stable/usage/general.html (BORG_HOST_ID 条目) |
| **Google Borg**(对照,弱证据) | 集群调度器的任务健康检查与重启策略与上列同构;**论文正文付费墙未抓取,不作强证据**,只标注公开页可达 | https://dl.acm.org/doi/10.1145/2741948.2741964 (EuroSys'15;正文未采,见 §5) |

横向归纳三条共性,恰是 cas-kb 缺的三块:(a) **心跳由工作者主动发**(sd_notify WATCHDOG=1),间隔与缺席处置事先约定;(b) **墙钟上限由外部强制**(timeout-minutes / RuntimeMaxSec / Slurm time limit),超时处置是平台动作而非人际判断;(c) **死亡后的资源回收有主/跨主机分级**(borg 僵尸锁),且 Slurm 把死亡记成可审计终态(TIMEOUT),不留悬案。

### 1.3 对 cas-kb 的建议:心跳纪律最小可行形态(T54 根因与预防写透)

**采纳「心跳 = commit + 墙钟上限 + 复活三查」;死亡判定自动化(一个脚本);复活协议成文;处置权归负责人(不自动 kill)。**

1. **心跳 = commit,给节奏上限**:AGENTS.md「写完的东西要立刻存档」已是 commit 纪律,缺的是**频率约束**——每个工作步(一次调研、一个可验证改动)完成后立刻 commit;无法 commit 的长动作(外网等待、长测试)不得超过 30 分钟,超时应拆步。**心跳间隔 ≤30 分钟**即最小可行形态:不需要新工具,分支 tip 的 commit 时间就是心跳。
2. **三条阈值(成文,不靠感觉)**:
   - 心跳间隔 ≤ **30 分钟**(一个工作步的上限);
   - 死亡阈值 = 最近 commit 年龄 > **90 分钟**(3× 心跳间隔,即「连续三拍缺席」,对齐 systemd watchdog「缺席即处置」的判据形态);
   - 会话墙钟上限 **6 小时**(对齐 GitHub Actions 默认 360 分钟;超限即使仍在产出也应收束或明确展期——展期即 EXTEND_TIMEOUT_USEC 的组织等价物:**显式说出,不留默认**)。
3. **负责人自动判定死亡(脚本化,退出码说话)**:git for-each-ref refs/heads --format='%(refname:short) %(committerdate:unix)' 对活跃分支算最近 commit 年龄,> 死亡阈值标 STALE、整体退出码非零,可手动跑也可挂 cron。落地为 scripts/session-watch.sh(§1.4)。判定归脚本,**处置归人**——不自动删分支、不自动合并(学 Slurm:死亡进终态记录,动不动刀由管理员)。
4. **复活协议(防 T54 重演,此为核心)**:复活 ≠ 续写原计划。任何「原会话复活/重建」开工前必须走三查,每查不过即转「已交付/归档」分支处理:
   - **查已交付**:ROADMAP 顶部状态行与对应里程碑小节、CHANGELOG(Unreleased 与已定版)、git log --all --grep=<里程碑关键词>——T54 场景下这一查即可终止撞车(M6-A「已交付」白纸黑字);
   - **查分支关系**:git branch --merged main 与 git log --oneline main..<分支>;分支已并入 main 时,复活的唯一合法动作是**归档**(改名/删除老分支),不产生任何新实现;
   - **查撞车**:git diff <复活分支>…main -- <目标文件>;main 已有同范围实现时,**复用优先于重写**——差距用小步补丁/cherry-pick 表达,禁止按旧计划重写第二套。
   复活产出的首个 commit message 注明「复活自 Txx,已核对 main@<sha> 无重复实现」(审计锚点);入库前照走 ./scripts/verify.sh 门禁与「文档三处同步」纪律。
5. **所有权互斥与冻结名单**:一会话一 worktree 一分支(现状已是),补两条成文:(a) 被判死亡的分支进入**冻结名单**(不新写),由负责人决定归档或重启;(b) 重启 = **新分支 + 复活协议**,不「原地复活」旧分支继续长——旧分支 tip 停在死亡时刻,本身就是事故现场的勘验证据。
6. **T54 根因链(逐条对应社区同构物)**:
   - **心跳缺失** → 停滞不可见,「死亡」只能靠人工猜测,且猜测不受任何时限约束(同构物:WatchdogSec/timeout-minutes 的「缺席有数、超时有界」);
   - **复活核对缺失** → 复活动作与主线状态脱钩,原计划被原样执行,而其交付物已在 main 上存在(同构物:borg 跨主机僵尸锁「绝不自动动,必须显式 break-lock」——对主线已有实现的处置必须经过显式核对这一步);
   - **所有权/交接记录缺失** → 没有「谁在做什么、做到哪」的可查台账,两个会话在同一范围并行生产无人察觉(同构物:Slurm 把每个作业的生死记成可审计终态,而不是靠记忆);
   - **放大器**:补交实现未经「已交付核对」即走整批入库流程,直到报废才暴露。根因修复的优先级:**复活三查 > 心跳阈值 > 冻结名单**——前两者直接切断事故链,后者防再犯。

### 1.4 落地改动清单草案(供裁剪)

- [ ] AGENTS.md(或 docs/ 工作约定文档)增「会话心跳纪律」一节:30/90/360 三阈值、复活三查、冻结名单、复活 commit message 锚点格式
- [ ] scripts/session-watch.sh:列活跃分支最近 commit 年龄,超阈值输出 STALE、退出码非零(供手动巡检与 cron 消费)
- [ ] (可选,捎带)drill 报告头部增一行「会话自证:最后 commit 时间」——报告已有工作树/HEAD 惯例,顺手成文
- **不做**:心跳写入产品库(kb 库不承载开发流程数据)、跨机心跳服务、自动 kill/自动删分支(判定归脚本,处置归负责人)

---

## 2. 调研项 2:合并双亲的结构化暴露

### 2.1 cas-kb 现状

- **HTTP**:GET /api/v1/log 行契约 = id/time/message/**parents**(internal/view/view.go 的 LogRow,Parents []string 短标识数组;注释明确「parents 为短标识数组,首元素即 CLI 的 parent= 列」);DESIGN §8.5 端点表该行已列「id/time/message/parents,短标识与 CLI 同长」。根提交 parents 为空数组(非 null)。
- **CLI**:kb log 文本行 parent=none / parent=<p1> / parent=<p1>,<p2>(逗号连接;cmd/kb/log.go 注明「追加第二亲短标识,与既有行格式兼容(first-parent 链不变)」)。
- **脚本契约**:scripts/drill-multi.sh 两条断言依赖该文本格式——grep -q "," 判「应双亲」、grep -qF "$bhead" 判「theirs 头在双亲中」(leg1/leg2)。
- 结论预告:**「是否增 parents 数组」已经发生过,答案是「已增」**(M5 合并交付时 LogRow 即带 parents);本节的真实问题是「契约还要不要再动」——见 §2.3,答案是不动。

### 2.2 社区通行做法与证据

| 系统 | 多亲表达 | 证据 |
|---|---|---|
| **GitHub REST / Gitea** | commit 对象 parents **数组**,合并提交即长度 2——拓扑事实原样暴露,不做语义解释 | **引用 T47 §1.2(已调研,勿重复检索)**:docs/research/best-practices-adoption.md §1.2(GitHub 实测 torvalds/linux 合并提交 parents 恰 2 项;Gitea swagger 实抓) |
| **git**(git log --format) | %P = **parent hashes**、%p = **abbreviated parent hashes**(git-scm pretty-formats 页原文);多亲时**空格分隔**;输出是平面文本列,无数组 | 实抓 https://git-scm.com/docs/pretty-formats (%P/%p 两条定义);**本仓实测双证据**:合并提交 43eead3(T53 归档合并)——%P → 「40ead222… d9c0589…」(两枚全哈希空格分隔),%p → 「40ead22 d9c0589」,git show -s --format='%H%n%P' 同样全哈希 + 空格分隔双亲 |
| **jj**(模板系统) | Commit 类型带 .parents() -> List<Commit> 方法;官方示例即多亲展示:parents.map(|c| c.commit_id().short())(取双亲短 id)、parents.any(...)/parents.all(...)(按双亲判定);默认 log 以图形式直接画多亲边 | 实抓 https://www.jj-vcs.dev/v0.39.0/templates/ (.parents() 方法签名与 map/any/all 示例原文) |

横向归纳:三系一致把「多亲」作为**结构化列表**暴露(HTTP 数组 / 空格分隔的格式化列 / 模板 List),从不塞进 message、不做语义改写;**分隔符只是展示层的选择,列表(数组)才是契约**。git 用空格、cas-kb 文本用逗号,属同一层的选择自由。

### 2.3 对 cas-kb 的建议

**契约不动;文本不动;增量只有两处文档注记、一条对称断言与一个可选 CLI 出口。**

1. **/api/v1/log JSON:保持 parents 数组现状,不再改。** 已满足向后兼容三要素:(a) 拓扑事实原样暴露(GitHub/Gitea 同型,T47 §1.2 的横向归纳 (c) 直接适用);(b) 根提交输出空数组而非 null,消费方统一按数组处理、无需特判;(c) 未来演进走「加字段不改义旧字段」——若需要全地址或亲序语义,新增 parents_full 之类新字段,parents 的含义与形态永不回填。GitLab merge_status 改名弃用的教训(T47 已引)在此同样适用:**一次定准,不动旧义**。
2. **CLI kb log 文本:逗号分隔保持不变。**(a) drill 断言依赖(grep -q ","),改分隔符即破坏既有脚本契约;(b) 行格式自 M5 起稳定,「first-parent 链不变」注释即兼容承诺;(c) git 的空格分隔是 %P 的选择而非 CLI 展示的普遍约定(jj 干脆用图),逗号不构成互操作障碍——机器消费应走 HTTP JSON 出口(下条补齐)。
3. **与 merge-state 的交叉引用(文档注记级,不加端点)**:合并收束后新快照 parents = [ours, theirs](首亲 = 本地头,次亲 = 远端头),恰与 GET /api/v1/merge-state 的 ours/theirs 字段对应(base 是 LCA,不进 parents——parents 是拓扑、base 是语义,分列是 T47 §1.2「状态查询与业务资源分离」的延续)。建议在 DESIGN §8.5 的 /api/v1/log 行补半句注记「parents 与 merge-state 的 ours/theirs 可交叉验证」,并在 drill leg1/leg2 补一条 ours 头断言(现有断言只验 theirs 头在双亲中,补对称项即闭环,无需新工具)。
4. **可选增量:kb log --json**(低优先):输出 view.LogRow,与 HTTP 同构(internal/view 一份实现两个出口,TestServeCLIParity 同法钉死)。HTTP 出口已存在,CLI 侧纯补齐;真实需求(Agent 免 shell 消费 log 的场景成规模)出现再立项。

### 2.4 落地改动清单草案(供裁剪)

- [ ] DESIGN §8.5 /api/v1/log 行注:parents=[ours,theirs] 与 merge-state 交叉验证说明(随下一文档批次捎带)
- [ ] scripts/drill-multi.sh leg1/leg2 增「双亲含 ours 头」断言(与 theirs 对称)
- [ ] (可选)cmd/kb log 增 --json(view.LogRow + parity 测试)
- **不做**:改文本分隔符、parents 改对象/加语义字段、merge-state 里塞 parents(拓扑与状态分列)

---

## 3. 调研项 3:回归调度惯例与 drill 产物归档

### 3.1 cas-kb 现状

- 调度:**无**(本机无常驻 CI;回归 = 手动跑 verify.sh;drill 两脚本 T50 固化后仍是手动触发)。
- 聚合:verify.sh 已是「gofmt/build/vet/test + e2e」聚合门禁;drill 按 T47 §3.3 裁剪**默认不进** verify(DRILL=1 选择性),季度/发版前「直接跑两个 drill」只停留在口头,无载体。
- 归档:演练报告 docs/review/ 下 drill-*.md 两份范本在案(头部日期/工作树/HEAD/环境,正文结论表 + 缺陷清单,尾行 VERDICT);临时产物限 /tmp、e2e 产物不入库(AGENTS.md 纪律)。
- 缺口:季度/发版前回归无调度载体(全凭记性);drill 的日志/时间戳产物无固定去处;「报告 vs 日志」两类产物的归档边界没有成文。

### 3.2 社区通行做法与证据

| 惯例 | 要点 | 证据(2026-09-02 实抓) |
|---|---|---|
| **cron**(季度调度) | 标准五字段列表语法即可表达季度:「Lists are allowed. A list is a set of numbers (or ranges) separated by commas. Examples: "1,2,5,9", "0-4,8-12"」——季度任务 0 2 1 1,4,7,10 *(每季首日 02:00)零扩展依赖 | https://man7.org/linux/man-pages/man5/crontab.5.html (Lists 与 Ranges 两段原文) |
| **GitHub Actions**(on.schedule) | POSIX cron 语法;**「Scheduled workflows will only run on the default branch」**;公开仓库**60 天无活动自动停用**;**不支持 @yearly/@monthly 等简写**;调度有队列延迟(不精确到秒) | 实抓 github/docs 仓库 raw markdown(events-that-trigger-workflows.md 的 schedule 节:四条 NOTE 原文 + 示例 cron: "15 4,5 * * *");渲染页:https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule |
| **launchd**(macOS 原生) | StartCalendarInterval 接受「**dictionary of integers or array of dictionaries of integers**」,缺省键为通配;子键 Month <integer> 为「The month (1-12) on which this job will be run」;StartInterval 与 StartCalendarInterval 互不感知 | **本机实测**:man launchd.plist(macOS 自带手册页)StartCalendarInterval/Month 原文;在线镜像:https://keith.github.io/xcode-man-pages/launchd.plist.5.html |
| **Makefile/GNU make**(目标聚合) | phony 目标是聚合入口的标准形态(clean: 示例 + .PHONY 声明防同名文件遮蔽);T47 §3.2 已引 git t/ 的 make 子集选择同构 | https://www.gnu.org/software/make/manual/html_node/Phony-Targets.html (phony 定义与 clean 示例原文) |
| drill 报告归档 | 社区通用做法是 CI artifacts 上传 + 保留期,**本机不可验,不采信**(§5 注);cas-kb 自己的两份 drill 报告即「报告归档」的既成惯例,直接升格成文 | docs/review/drill-multi-cli.md、docs/review/drill-serve.md(头部元信息/结论表/VERDICT 尾行三要素齐备) |

### 3.3 对 cas-kb 的建议:无常驻 CI 现实下的最低成本调度

**聚合脚本 + launchd 季度任务 + 发版前 checklist 三层;不建常驻 CI,不引外部服务。**

1. **scripts/regression.sh(聚合器,第一层)**:依序跑 verify.sh → drill-multi.sh → drill-serve.sh,逐段记 ok/not ok(学 drill 的 TAP 汇总与 verify 的门禁纪律:任一非零 ⇒ 整体退出非零);输出 tee 到带时间戳日志(regression-YYYYMMDD-HHMMSS.log,默认临时目录/logs/,不入 git——e2e「产物不入库」同款)。形态上就是 Makefile phony 聚合目标的 shell 版,与 verify.sh 同风格,零新依赖。
2. **launchd 季度任务(第二层)**:StartCalendarInterval 四个字典(Month = 1/4/7/10,Day = 1,Hour = 2,Minute = 0);ProgramArguments 用**数组逐参数**(不写 sh -c 复合字符串,从根上规避 T43 D1 的 XML && 转义问题);StandardOutPath/StandardErrorPath 指向固定日志目录。**交付前必须 plutil -lint**(T43 D1 教训制度化:任何 plist 入 docs 前先过 lint,验证记录随交付)。失败即日志可见 + 退出码非零,负责人季度巡检一次即可。
3. **cron(Linux 备选,不设为默认)**:本机是 macOS,launchd 原生;若未来回归脚本迁到 Linux 主机,0 2 1 1,4,7,10 * 一行即可,列表语法无需任何扩展(crontab(5) 证据)。
4. **GitHub Actions schedule(远备,默认不做)**:仓库在 GitHub,如希望脱离本机可加 workflow(schedule + workflow_dispatch 双触发,后者保手动兜底)。三条注意直接来自文档:只在默认分支跑(调度不认 feature 分支)、公开仓库 60 天无活动自动停用(**cas-kb 交付节奏可能触发,需人工复查**)、不支持 @yearly 简写(写全五字段)。默认不做——无常驻 CI 是现状约束也是成本选择,本条仅立此存照。
5. **发版前回归 checklist(第三层,把记性写成字)**:发版前必跑 ./scripts/regression.sh,报告归档 docs/review/(沿 drill-*.md 惯例:日期/工作树/HEAD/结论表/VERDICT);README「开发与测试」补回归行(用法 + 频率:季度 + 发版前)。
6. **产物归档约定成文(两类,边界一句)**:**报告**入 docs/review/(入 git,结构化:日期/工作树/HEAD/结论/VERDICT——两份 drill 报告即范本);**日志**带时间戳(不入 git,临时目录或固定 logs/ 目录,定期清理)。「报告讲结论、日志留现场」,与 e2e「产物不入库」纪律同构。

### 3.4 落地改动清单草案(供裁剪)

- [ ] scripts/regression.sh:verify + drill×2 聚合,TAP 式逐段汇总,时间戳日志,失败非零退出
- [ ] launchd 季度回归任务 plist(StartCalendarInterval 1/4/7/10;ProgramArguments 数组式;日志目录)+ plutil -lint 验证记录(入库前)
- [ ] README「开发与测试」补回归行:regression.sh 用法、季度/发版前频率、报告与日志两类归档约定
- **不做**:常驻 CI、drill 进默认门禁(T47 已裁剪,维持)、artifacts/外部归档服务

---

## 4. 实施清单(优先级排序,供负责人直接立项)

| 项 | 内容 | 优先级 | 预估规模 | 依赖 | 对应节 |
|---|---|---|---|---|---|
| T57-A | 会话心跳纪律成文(AGENTS.md/工作约定:30/90/360 三阈值 + 复活三查 + 冻结名单 + 复活 commit 锚点格式) | **高**(T54 类事故的流程闭环,纯文档) | 小(一节文档) | 无 | §1 |
| T57-B | scripts/session-watch.sh 停滞自动判定(活跃分支最近 commit 年龄,超阈值 STALE + 非零退出) | **高**(负责人巡检从记性变脚本) | 小(一脚本) | T57-A 的阈值定义 | §1 |
| T57-E | scripts/regression.sh 回归聚合器(verify + drill×2,时间戳日志,失败非零) | **中**(季度/发版前回归的执行载体) | 小-中(一脚本 + README 行) | 无(drill 已交付) | §3 |
| T57-F | launchd 季度回归任务(plutil -lint 前置,ProgramArguments 数组式) | **中**(无人值守的最后防线) | 小(一 plist + lint 记录) | T57-E | §3 |
| T57-G | 发版前回归 checklist 与「报告/日志」两类归档约定成文(README) | **中**(把既成惯例写成字) | 小(README/文档行) | T57-E | §3 |
| T57-C | DESIGN §8.5 log 行注「parents ↔ merge-state 交叉验证」+ drill 补 ours 对称断言 | **低**(契约不动,只补注记与对称断言) | 小 | 无;随任一文档/测试批次捎带 | §2 |
| T57-D | kb log --json(可选;view.LogRow 同构 + parity 测试) | **低**(HTTP 出口已在,CLI 补齐属锦上添花) | 小 | 无 | §2 |

排序理由:T57-A/B 直接切断 T54 事故链(复活撞车),纯流程 + 小脚本,先落且互为表里(先成文后脚本化);T57-E/F/G 构成回归调度最小面,drill 已交付即可动工,E 是载体、F/G 各一小步;T57-C/D 是 §2 的低优先增量——契约与文本均已定准,只剩注记与对称断言,捎带即可,勿单独立项。

---

## 5. 证据与检索方法附注(诚实清单)

- 全部外链为 2026-09-02 实抓(curl 或等价 HTTP GET);关键断言尽量给「官方文档 + 实测」双证据:git %P/%p 用**本仓合并提交 43eead3 实测**(全哈希/短哈希、空格分隔双亲),launchd 用**本机 man launchd.plist 实测** + 在线手册镜像双源,GitHub Actions 两节取自 github/docs 仓库 raw markdown(官方文档仓库,内容与渲染页同源)。
- **检索不到/未采信的项,如实记录**:
  - Google Borg 论文正文付费墙(ACM DOI 页可达、正文未抓):§1.2 只作对照注记,不作强证据;「Borg 心跳」的定性结论全部落在可实抓的 borgbackup/Slurm/systemd 证据上;
  - docs.github.com 渲染页对抓取器不可见(SPA 空壳),GitHub Actions 证据改取 github/docs 仓库 raw markdown(workflow-syntax.md 72KB / events-that-trigger-workflows.md 68KB,均全文到手);仓库内旧路径(writing-workflows/workflow-syntax-for-github-actions.md)已 404,文档结构迁移属实,引用一律给渲染页新链接;
  - jj 主干 docs/templates.md 与 GitHub 镜像抓取超时,证据取自 jj-vcs.dev v0.39.0 在线版 templates 页(.parents() 签名与 map/any/all 示例原文到手,足够支撑结论);
  - GitHub Actions artifacts 的归档保留期细节:未实抓,§3.2 明确标注「不采信」,归档建议全部落在 cas-kb 既成惯例上;
  - borgbackup「同主机僵尸锁自动回收」的行为描述取自 BORG_HOST_ID 文档的因果注记("kills automatic stale lock removal"),未做实机复现;
  - T54 复活补交撞车的经过为本任务输入背景,仓库内无独立事故记录文件——§1.1 的还原按输入如实标注,根因链(§1.3-6)是分析而非新事实。
- 本文件为 T57 唯一交付物;未修改任何代码与既有文档;不推送、不合并。
