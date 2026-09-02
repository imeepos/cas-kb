# cas-kb 社区最佳实践采纳调研(T47)

> 任务:T47 纯文档调研(不含任何代码)· 分支 `research/best-practices` · 交付物 = 本文件(唯一新增,不修改任何代码与既有文档)
> 检索日期:2026-09-02;全部证据链接为本次检索实测取得(curl / 线上 API / 官方文档抓取),「检索不到」的项如实标注,不做编造
> 对象:五个待办/复核项——①合并状态 HTTP 暴露 ②多端冷启动 ③演练脚本固化 ④索引段化观测指标 ⑤健康自检命令
> 每项结构:**cas-kb 现状 → 社区通行做法(证据)→ 建议(采纳/改造/不采纳+理由)→ 落地改动清单草案**;§6 汇总为实施清单表供负责人直接立项

## 0. TL;DR

1. **合并状态 HTTP 暴露(建议采纳,改造)**:新建 `GET /api/v1/merge-state` 单端点(不并入 projects),状态字段最小集 = 单枚举 `state` + 派生布尔(`can_continue`/`can_abort`)+ base/theirs/ours 地址 + 冲突数;无合并中态返回 **200 + `state:"idle"`**(可轮询),项目/分支不存在才 404。依据:K8s 健康端点「以状态码为准」、Gitea「单枚举 + 布尔派生」、GitLab「语义含糊的 merge_status 在 15.6 被弃用」的反面教材。
2. **冷启动旗标(建议维持显式,不升默认)**:git 官方立场明确——`--allow-unrelated-histories` 默认拒绝且「no configuration variable to enable this by default exists or will be added」;jj 证明无旗标可行但代价是拼库风险前置到用户。cas-kb 采纳 git 立场 + 学 Syncthing 的「双向确认式」冷启动指引文案,把三步可行动输出写顺。
3. **演练剧本固化(建议采纳)**:git t/ 套件的 TAP 式输出(ok/not ok 逐行 + 末尾汇总)、per-test 隔离临时目录、`--run` 子集选择、trap 清理,正是 drill-multi/drill-serve 报告缺的「可重复执行」形态;落地为 `scripts/drill-*.sh`,默认独立跑,verify.sh 以 `DRILL=1` 选择性接入(不进默认门禁,守住时长)。
4. **索引段化观测(只落指标清单,不设计实现)**:bleve scorch 的 Stats 结构体给出段化引擎「该观测什么」的行业模板(gauge/counter 分类、根段数、merge 写字节、最大 merge 耗时);tantivy LogMergePolicy 的旋钮全是「每段文档数 + 删除比」。cas-kb 落 DESIGN §7 观测清单草案:6 个指标 + 采集点 + 触发线,等真实 workload 数据再立项段化。
5. **kb doctor(建议采纳,规模小)**:brew doctor 的形态可直接照搬(检查项可 `--list-checks` 列举、可单独跑、有问题非零退出);git fsck 已是 cas-kb 现成内核。注意:**社区常引用的 `gh doctor` 在当前 gh CLI 中不存在**(经 cli/cli 主干文件树与按路径提交历史双重核实,见 §5.2)——如实记录,不以讹传讹。

---

## 1. 调研项 1:合并/运维状态的 HTTP 暴露

### 1.1 cas-kb 现状

- 合并中间态(DESIGN §6.3 / docs/research/merge-design.md §2.5.2):冲突时落 `<branch>-merge` 分支(基线快照,Message=`merge base`,不建索引)+ meta 键 `merge.<项目>.<分支>`(单键 JSON,含 base/theirs/ours 地址与冲突清单);期间该分支直接写全部冻结,`--stage` 升格为裁决,`kb merge --continue | --abort` 收束。
- CLI 侧可观测:`kb stage` 在合并中态切换为裁决清单展示;`kb log` 合并行显示双亲短标识;冲突清单在 pull 时以退出码非零 + 逐行(路径/类别/base/ours/theirs)输出,`--json` 为结构化契约。
- HTTP API(DESIGN §8.5/§8.6,已交付):`/healthz` + `/api/v1/{projects,tree,note,search,log,diff}`(全 GET)+ `POST/DELETE /api/v1/note`(令牌制)。**合并中态在 HTTP 面完全不可见**;serve 写端点受冻结纪律影响(拒绝写)但客户端只能从报错文案反推。
- merge-design.md §4-9 明确留下的开放问题:「合并要不要进 HTTP API?v1 不暴露……开放:最小只读暴露(`GET /api/v1/merge/status`)还是完全 CLI-only」。本节即对该开放问题给方案。

### 1.2 社区通行做法与证据

| 系统 | 状态表达 | 证据(2026-09-02 实抓) |
|---|---|---|
| **GitHub REST**(提交拓扑) | commit 对象带 `parents` 数组,合并提交即数组长度 2——拓扑事实直接暴露,不做语义解释 | 文档:docs.github.com/en/rest/commits/commits#get-a-commit;实测 `GET api.github.com/repos/torvalds/linux/commits/master` 返回 `parents` 恰为 2 项(合并提交) |
| **GitHub REST**(PR 状态机) | `state`(open/closed)+ `merged` 布尔 + `mergeable` 布尔(true/false/**null=计算中**)+ `mergeable_state`(细化态)——**单枚举 + 派生布尔**双层 | 文档:docs.github.com/en/rest/pulls/pulls;实测 `GET api.github.com/repos/octocat/Hello-World/pulls/1` → `state=closed, merged=false, mergeable=false, mergeable_state=dirty`;`mergeable_state` 取值语义的社区参考:github.com/orgs/community/discussions/21886 |
| **Gitea**(PR 状态机) | `state` 枚举只有 `open`/`closed`,**merged 是独立布尔字段**而非枚举值;另有 `mergeable` 布尔与 `merge_base`(基准 SHA) | 实抓 swagger:https://gitea.com/swagger.v1.json(PullRequest 定义:`state` enum=[open, closed]、`merged` boolean、`mergeable` boolean、`merge_base` string) |
| **GitLab**(反面教材) | `merge_status` 因语义长期含糊,**GitLab 15.6 官方弃用,改推 `detailed_merge_status`**;冲突以 `has_conflicts` 布尔暴露(文档明确:「Returns false unless merge_status is cannot_be_merged」) | 实抓 gitlab.com/gitlab-org/gitlab/-/raw/master/doc/api/merge_requests.md(弃用注记与 has_conflicts 定义均在文中) |
| **Kubernetes**(健康端点约定) | `healthz`/`livez`/`readyz` 三端点分离,healthz 自 v1.16 起废弃;**调用方只依赖 HTTP 状态码(200=健康)**,状态码之外的信息仅作人读补充;支持 `?verbose` 与逐项检查 | https://kubernetes.io/docs/reference/using-api/health-checks/ |
| **Prometheus**(`/metrics` 惯例) | 经 HTTP 文本格式暴露(`Content-Type: text/plain; version=0.0.4`);命名约定 MUST 应用前缀、MUST 单一单位、SHOULD 基础单位(seconds/bytes)、counter 用 `_total` 后缀 | https://prometheus.io/docs/instrumenting/exposition_formats/ 与 https://prometheus.io/docs/practices/naming/ |

横向归纳四条通行做法:(a) 状态查询与业务资源分离(K8s 健康端点、Prometheus /metrics 都是独立端点);(b) 状态机用「单枚举 + 派生布尔」双层(GitHub/Gitea 一致);(c) 拓扑事实(parents)原样暴露,不加工;(d) 状态字段语义必须一次定准——GitLab 的弃用史说明事后改名代价极高(API 契约变更即破坏性变更)。

### 1.3 对 cas-kb 的建议

**采纳「独立端点 + 单枚举 + 派生布尔」;不采纳「并入 projects 分支列表」;改造点在 404 语义(与三大托管平台不同,cas-kb 的合并态是「分支的属性」而非「一个可消失的资源」)。**

1. **形态:新建 `GET /api/v1/merge-state?project=<项目>&branch=<分支>`,不并入 `/api/v1/projects`。**理由:(a) projects 端点现与 `project ls --json` 同构(TestServeCLIParity 钉死),塞入每分支的合并态会破坏这份既有契约;(b) K8s/Prometheus 的先例都是状态独立成端点,轮询方的心智模型与实现(定时 GET 一个 URL)都更简单;(c) merge-design §4-9 预想的最小只读暴露正是单端点,范围控制与 §8.6「恰好两个写端点」的先例一致。省略参数时取 serve 进程的 `KB_PROJECT`/默认分支,与现有端点参数习惯一致。
2. **状态字段最小集(单枚举 + 派生布尔 + 事实字段)**:

   ```json
   {
     "project": "default",
     "branch": "main",
     "state": "merging",            // idle | merging(单枚举,学 Gitea 的克制)
     "can_continue": true,          // 派生布尔,state==merging 时 true(GitHub mergeable 同型)
     "can_abort": true,
     "base": "sha256:…",            // 合并中态事实字段(无则 null):LCA 基准
     "theirs": "sha256:…",
     "ours": "sha256:…",
     "conflicts": [                 // 与 CLI 冲突清单同构(--json 契约复用 internal/view)
       {"path": "workflow/inbox", "kind": "content", "base": "…", "ours": "…", "theirs": "…"}
     ],
     "conflict_count": 1,
     "merged_branch": "main-merge"  // 中间态分支名,供下一步 CLI/API 操作引用
   }
   ```

   关键取舍:`conflicts` 逐条复用 pull `--json` 冲突清单的 `{path, kind, base, ours, theirs}` 契约——一份实现(internal/view)两个出口,延续 TestServeCLIParity 纪律;`state:"idle"` 时事实字段为 null、`conflicts` 为空数组。**不暴露** GitLab 式语义含糊字段,也暂不做 mergeable_state 式多值细化态(cas-kb 的「能否收束」由冲突清单与冻结纪律唯一决定,两布尔即完备);多值细化态留给真实需求出现再扩,避免 GitLab 式返工。
3. **404 vs 200+empty:**`state:"idle"`(无合并中态)返回 **200**;仅当项目或分支不存在才 **404**。理由:merge-state 的主要消费者是自动化轮询(Agent 在收束前反复查询),「没有进行中的合并」是正常稳态而非错误——404 会把稳态变成错误流,迫使轮询方特判;这与 /api/v1/note 的 404(资源本身不存在)语义正交:note 查询的是「库里有没有这条」,merge-state 查询的是「这个必然存在的分支现在处于什么状态」。K8s「以状态码表达健康、200 即正常」的纪律照搬至此:200 + idle = 健康、无合并。

### 1.4 落地改动清单草案(供裁剪)

- [ ] internal/server 新增 `GET /api/v1/merge-state`(只读;读 `<branch>-merge` 分支存在性 + meta 键,无任何写路径;受 -p/KB_PROJECT 作用域约束,显式 `?project=` 可跨项目查询)
- [ ] internal/view 增 MergeStateRow 契约;conflicts 复用合并冲突清单行契约(字段名/顺序/短标识派生一份实现)
- [ ] CLI `kb stage` 补 `--json`(若尚无)——CLI 与 HTTP 出口同构,parity 测试钉死(TestServeCLIParity 扩展)
- [ ] 文档三处:DESIGN §8.5 端点表加行(标注「只读、非 GET 405、错误响应 `{"error":…}`」沿表惯例);merge-design.md §4-9 开放问题闭合指向本文;CHANGELOG
- [ ] 错误语义测试:项目不存在 404 / 分支不存在 404 / idle 200 / merging 200(字段逐项)/ 非法参数 400
- **不做**:合并的写端点(POST merge/abort)——冻结收束动作继续 CLI-only,与 §8.6 范围控制先例一致;待真实自动化需求出现再立项

---

## 2. 调研项 2:多端冷启动 / 首次同步

### 2.1 cas-kb 现状

- 两库各自 `kb init`(无共同历史)时:`pull` 默认拒绝且文案分流(T44)——提示 `--force` 覆盖,或 `--merge --allow-unrelated` 做空基线合并(不再笼统指路 `--merge` 造成指引断裂,见 drill-multi-cli.md D2 的「矛盾证据」);远端项目存在但分支不存在(零提交)→「已是最新」空操作(v0.6.1 修复 drill-multi-cli.md D1)。
- `--allow-unrelated`(v0.6.1 已交付):**仅与 `--merge` 连用**,单独给或与 `--force` 同给响亮拒绝;有共同祖先时该旗标不改变任何行为;空基线合并以空树为基准、判定完全复用既有判定表,冲突清单 base 列标注「空基线」(DESIGN §6.3)。
- README「多机同步与三方合并」段已有一行用法示例(`pull … --merge --allow-unrelated`);无专门「冷启动」指引段落。

### 2.2 社区通行做法与证据

- **git:`--allow-unrelated-histories` 默认拒绝,且官方明确不提供配置变量。**git-merge 手册原文(2026-09-02 实抓 https://git-scm.com/docs/git-merge ):
  > "By default, git merge command refuses to merge histories that do not share a common ancestor. This option can be used to override this safety when merging histories of two projects that started their lives independently. **As that is a very rare occasion, no configuration variable to enable this by default exists or will be added.**"

  语义要点:该旗标自 git 2.9(2016)引入,是「安全阀」而非「开关」——官方判断该场景罕见到不值得给默认化配置,静默合并无关历史的风险(把两个不相干的项目缝成一个)必须由显式动作承担。jj 场景文章(dev.to/msmetko/how-to-merge-two-repositories-with-jj-4691)同样把 git 的这条旗标列为第一对照:「only if you don't have conflictingly named files (or you like resolving git conflicts)」。
- **Syncthing:两台全新设备的 bootstrap = 生成设备 ID → 双向互填 → 首次全量同步。**官方 Getting Started(实抓 https://docs.syncthing.net/intro/getting-started.html ):首启日志打印 `My ID: 6FOKXKK-…`;随后「To get your two devices to talk to each other click "Add Remote Device" at the bottom right **on both devices**, and enter the device ID of the other side… At this point the two devices share an empty directory. Adding files to the shared directory on either device will synchronize those files to the other side.」——流程要点:(a) 每台设备开箱即自证身份(ID 由密钥派生,无需注册);(b) **配置必须双向**(文档原文「the configuration must be mutual for a connection to happen」);(c) 空目录握手成功后,数据层自动全量收敛,用户无第三个动作。
- **Jujutsu(jj):无共同历史不设旗标,合并无关仓库是日常操作。**工作副本提交模型下一切命令都在建 DAG 节点,跨仓库合并的社区工作流(同上 dev.to 实文):`jj git remote add <name> <url>` × 2 → `jj git fetch --all-remotes` → `jj new master@personal-reusables` 直接以另一仓库分支为父建提交——没有任何「unrelated histories」检查点(jj 官方手册亦无该旗标概念)。

### 2.3 对 cas-kb 的建议

**旗标维持显式(采纳 git 立场);不升默认(不学 jj);冷启动指引文案改造为「Syncthing 式三步走」。**

1. **`--allow-unrelated` 保持显式,反对两个方向的「改进」**:(a) 反对升默认/加配置变量——git 官方用十年坚持的判断(罕见 + 静默拼库风险)对 cas-kb 同样成立,且 cas-kb 的目标用户含自动化 Agent,默认放开会让误配的两个库静默缝合成一个,违背「响亮失败」纪律;(b) 反对学 jj 完全去旗标——jj 的安全垫是「工作副本提交 + 可 abandon 任意反悔」,cas-kb 的 pull 直接推进分支指针,反悔成本高一个量级。保留旗标纪律(仅与 `--merge` 连用、有共同祖先时无效)不动。
2. **冷启动指引文案改造(改造采纳 Syncthing)**:Syncthing 的可学之处不在机制而在**文案结构**——「自证身份 → 双向确认 → 数据自动收敛」三步,每步给出用户可见的锚点。cas-kb 对应写法(建议落 README「多机同步」段 + `pull` 报错文案):
   - 第 1 步(自证):两台机器各自 `kb init` + 写入——各自形成独立历史,这是正常起点,不是错误;
   - 第 2 步(对拉):**任意一台**执行 `kb pull <对端DSN> --merge --allow-unrelated`(与 Syncthing 的 mutual 不同,机械上单向即可;但文案要明说「先 pull 的一台合并后,对端再 pull 就走 fast-forward」——两次 pull 各一次,第二台不再需要旗标);
   - 第 3 步(收敛):零冲突直接落双亲合并快照,此后两库有共同历史,一切恢复正常同步语义。
   - 锚点提示:合并成功输出追加一行「冷启动完成:两侧历史已建立共同祖先,后续 pull 无需 --allow-unrelated」。
3. **文案分流保持现状**(T44 已修的「无共同历史 → 提示 --force 或 --merge --allow-unrelated」判定矩阵行不动)——drill-multi-cli.md D2 的「指引断裂」已闭合,本次只做「顺」度的打磨:报错文案里两条出路各配一句代价说明(`--force` 会丢弃本地独有提交;`--allow-unrelated` 做空基线合并、两侧新增互不冲突即全取)。

### 2.4 落地改动清单草案(供裁剪)

- [ ] README「多机同步与三方合并」段增「冷启动(两库各自 init)」三步指引(§2.3-2 文案)
- [ ] `pull` 无共同历史报错文案:两条出路各配代价说明;合并成功输出追加「冷启动完成」提示行
- [ ] docs/serve.md(或运维文档)「多机部署」处交叉引用冷启动段
- [ ] e2e.sh 增冷启动段(两临时库各自 init 写入 → A pull B --merge --allow-unrelated → B pull A 走 fast-forward → fsck)
- **不做**:`--allow-unrelated` 默认化、配置变量、jj 式去旗标——维持 v0.6.1 旗标纪律

---

## 3. 调研项 3:冒烟/回归脚本范式(drill 剧本固化)

### 3.1 cas-kb 现状

- `scripts/` 现有四件:verify.sh(gofmt/build/vet/test + e2e,单一质量门禁)、e2e.sh(`set -euo pipefail`、`mktemp -d` + `trap cleanup EXIT`、PATH 补齐、SQLite/PG 双模式、产物不入库)、backup.sh、restore.sh。
- 演练(drill)目前是**一次性手工操作**:T42(drill-multi-cli.md,多端互写合并,腿 1 因 D1/D2 BLOCKED、腿 2/3 PASS)、T43(drill-serve.md,serve 运维,PASS-WITH-DEFECTS P1-P5)——报告质量高但剧本本体不可重复执行,回归只能靠人工重走;T42 暴露的 D1/D2 修复(v0.6.1)也缺同场景自动复验。

### 3.2 社区通行做法与证据

- **git t/ 测试套件**(实抓 https://github.com/git/git/blob/master/t/README ):
  - 组织:编号测试文件(t0000-basic.sh、t1004-read-tree-…),每文件独立可执行,Makefile 支持按名选子集(`make *checkout*`);
  - 断言粒度:`test_expect_success 'test title' '…test body…'`,**标题与断言一一对应**,输出 TAP 式逐行 `ok N - title` / `not ok N - title`,末尾汇总 `# passed all remaining 42 test(s)` + `1..43` 计划行;
  - 隔离:每个测试脚本自动建 trash directory(临时目录)承载全部现场,失败时可保留供诊断(`-d/--debug`、`-i/--immediate` 首败即停);
  - 子集与跳过:`--run=<编号>` 只跑子集;`skip_all` + `test_have_prereq` 声明式跳过(如缺 PERL 则整脚本跳过并明说);
  - **trap 清理实证**(t/test-lib.sh):压力模式下 `trap 'kill $job_pids 2>/dev/null; wait; …' TERM INT HUP`,正常路径由 test_done 统一清理 trash——清理永远挂在信号/退出钩子上,不依赖每处手工调用。
- **Google Shell Style Guide**(https://google.github.io/styleguide/shellguide.html ):函数头注释块强制声明 Globals/Arguments/Outputs/Returns,并给出 cleanup() 的标准注释示例——脚本可读性约定。
- **非零即失败与输出摘要**:`set -euo pipefail` 是 cas-kb 现有约定(与 shellguide 的 strict mode 建议一致);git t/ 的「逐行 ok/not ok + 末尾汇总」即 TAP(Test Anything Protocol,testanything.org)——冒烟脚本输出对齐此风格即可被人和 CI 同时消费。

### 3.3 对 cas-kb 的建议

**采纳 git t/ 的四件套(逐条断言带标题、临时目录隔离、trap 清理、TAP 式汇总),改造为 cas-kb 的 drill-*.sh;接入 verify.sh 用环境旗标可选化(不采纳强制进默认门禁)。**

1. **结构**:`scripts/drill-multi.sh`(对应 T42 剧本:双库冷启动 → 分叉 → 冲突 → 裁决 → continue/abort → backup/restore)与 `scripts/drill-serve.sh`(对应 T43 剧本:默认绑定 → 令牌闭环 → 503 → 文档核验)。每腿 = 一组带标题的断言函数,输出 `ok N - <腿标题>` / `not ok N - <腿标题> (原因)`,末尾 `PASS x / FAIL y` 汇总行 + 以失败数决定退出码(非零即失败,FAIL>0 ⇒ exit 1)。
2. **参数化点**(全走环境变量,零参可跑):`KB_BIN`(被测二进制,默认现场 `go build`)、`DRILL_PORT`(serve 腿,默认 127.0.0.1:18787;`:0` 内核分配对齐 §8.5 测试惯例)、`DRILL_KEEP=1`(保留现场目录供诊断,对齐 git t/ 的 -d)、`DRILL_RUN=<编号>` 子集(对齐 git t/ --run)。临时目录一律 `mktemp -d` + `trap cleanup EXIT`(e2e.sh 现成范式);serve 进程清理挂 `trap 'kill $SERVE_PID …' EXIT INT TERM`(对齐 test-lib.sh 实证)。
3. **与 verify.sh 的关系**:**默认不进** `./scripts/verify.sh`(门禁时长敏感,drill 含外网检查与多进程并发写,放默认会让每次提交门禁翻倍);`DRILL=1 ./scripts/verify.sh` 选择性追加;季度跑/发版前跑直接 `./scripts/drill-multi.sh && ./scripts/drill-serve.sh`。serve 腿的令牌核验、503 制造(外部持 BEGIN EXCLUSIVE)依赖平台细节,脚本内先做前置探测(lsof/sqlite3 缺失 → skip_all 式明说跳过,对齐 git t/ prereq),**跳过 ≠ 失败**(不进汇总分母,但汇总行要显示 skipped 数)。

### 3.4 落地改动清单草案(供裁剪)

- [ ] `scripts/drill-multi.sh`:腿骨架 = 冷启动合并(§2.4 的 e2e 段可复用)/ 真实冲突裁决 / 冻结拒绝 / --abort 回滚 / backup→restore 往返;断言计数器 + ok/not ok 逐行 + PASS/FAIL 汇总;trap 清理两库与二进制
- [ ] `scripts/drill-serve.sh`:只读基线 / 令牌写入闭环 / 鉴权矩阵 / 503(可选,sqlite3 缺失即 skip)/ 默认绑定 lsof 核验(lsof 缺失即 skip);serve 生命周期 trap
- [ ] 两脚本共用头部(mktemp/trap/计数器/输出函数)——先内联复制,三个以上共用点再抽 lib(避免过早抽象)
- [ ] verify.sh 增 `DRILL=1` 追加段;README「开发与测试」表补 drill 两行(默认独立跑,季度/发版前)
- [ ] 用固化脚本复验 v0.6.1 修复(D1/D2 回归),结果写回 docs/review/(新报告文件,与本文互不影响)
- **不做**:引入 bats/shunit2 等测试框架(git t/ 证明纯 shell + 纪律足够,且引入依赖违背零依赖交付);把 drill 纳入 CI 默认流水线

---

## 4. 调研项 4:索引分段的可观测指标

### 4.1 cas-kb 现状

- 索引形态(DESIGN §7):64 固定桶(FNV-1a % 64)的 indexroot/indexshard,写入按新旧树叶子差集重写受影响桶;单条写放大实测在案——2000 条中文语料:单条 SetNote 95ms/条(累计 103s)、库 6.68GB(历史索引随快照冻结,O(N²));`bulk import` 350ms / 11.1MB;读路径 46-58ms 不受影响。
- 已有「三难权衡定论」(§7):现架构取「读快 + 历史可复现」,写慢由 bulk 绕开;**段化(文档分区)触发条件已定性**——「真实 workload 变为写多读少且可接受检索劣化」或「≥数万条」——但**没有观测清单**:靠什么数据判定已到触发线,目前无定义。
- 相关既有机制:SQLite 索引对象透明压缩(−60%);`gc --keep-last K` 精简历史索引;`index rebuild` 全量重建(自愈)。

### 4.2 社区通行做法与证据

- **bleve scorch(段化引擎)的 Stats 面**(实抓 https://github.com/blevesearch/bleve/blob/master/index/scorch/stats.go ):Stats 结构体注释明确分类纪律——「fields prefixed like **CurXxxx are gauges**(可升可降),prefixed like **TotXxxx are monotonically increasing counters**」;与段决策直接相关的字段:`TotFileSegmentsAtRoot`(根层段数)、`TotIntroducedItems/TotIntroducedSegmentsBatch`(每批引入的条目/段数)、`TotFileMergeWrittenBytes`(merge 写盘字节)、`MaxFileMergeZapTime`(最慢 merge 耗时)、`TotIndexTime/TotAnalysisTime`(索引耗时)、`MaxBatchIntroTime`(最慢批次引入耗时)、`TotPersistLoopWait`(落盘等待)。结论:**段化引擎自带的观测面 = 段数(gauge)+ merge 活动计数器 + 关键路径最慢值**,与「业务方看什么决定要不要段化」完全对齐。
- **tantivy LogMergePolicy 的决策旋钮**(实抓 https://docs.rs/tantivy/0.20.0/tantivy/merge_policy/struct.LogMergePolicy.html ):「LogMergePolicy tries to merge segments that have a similar number of documents」——全部旋钮围绕**每段文档数分层**:`set_min_layer_size`、`set_max_docs_before_merge`、`set_min_num_segments`、`set_level_log_size`,外加 `set_del_docs_ratio_before_merge`(删除文档占比触发 merge)。结论:段策略的输入变量只有两个——**段内文档数**与**删除比**;对应 cas-kb 即「索引对象数/单写库体积」与「无效词频占比」。
- **Prometheus 侧约定**(§1.2 同源):指标命名 MUST 应用前缀、MUST 单一单位、SHOULD 基础单位,counter `_total` 后缀——若未来暴露 /metrics,命名先行规范。

### 4.3 对 cas-kb 的建议

**采纳 bleve 的观测面模板 + tantivy 的变量集,落成 DESIGN §7 观测清单草案;不设计任何实现,不改 §7 三难定论(数据没到触发线之前,段化讨论不重启)。**

DESIGN §7「观测清单草案」建议增补如下(六指标,均可由现成命令/压测脚本采集,无需改产品):

| # | 指标 | 口径 | 采集点 | 触发线(线索,非承诺) |
|---|---|---|---|---|
| 1 | 单条写延迟 P99 | `note set` 端到端耗时,按库内条目数分桶记录 | 压测脚本(循环 + 计时);对照 §7 实测基线(2000 条 = 95ms/条) | P99 随条目数线性外推至交互不可接受(§7 定性线:约 100ms/条封顶) |
| 2 | 库体积及增速 | 库文件/PG 库占用,分「数据对象 vs 索引对象」两列(按对象 kind 聚合 size) | `kb fsck` 扩展输出或一次性统计查询;压缩后口径 | 历史索引占比 > 80% 且绝对体积进入运维红线 |
| 3 | 单次写索引重写字节 | 每次单条写实际重写的 indexshard 字节(≈写放大) | 压测脚本从 store 层统计;或 GC 前后对象计数差 | 恒定接近全索引字节(§7 实测:单条写≈全索引)即触及「写慢」极值 |
| 4 | 索引对象数 / 快照 | indexshard 对象数 × 快照数(gauge,bleve TotFileSegmentsAtRoot 同型) | objects 按 kind 计数 | 对象数增速显著超快照数增速(结构共享失效信号) |
| 5 | bulk 吞吐与单条写的比值 | bulk import N 条耗时 ÷ N vs 指标 1 | 压测脚本(2000 条基线:350ms 全批 vs 95ms/条) | 比值持续扩大 = bulk 缓解失效,段化收益上修 |
| 6 | 检索延迟 P95(含 --at) | search 端到端,现快照与历史快照分开记 | 压测脚本(基线 46-58ms) | 段化方案的已知代价(读全索引 5-10 倍劣化)出现前先有基线 |

两条纪律:(a) 指标 1/3/5 是**决策指标**(直接对应段化触发条件),2/4/6 是**护栏指标**(防止在别的轴上悄悄恶化);(b) 全部指标记录口径于文档,采集脚本属压测/观测工具,不入 verify 门禁。

### 4.4 落地改动清单草案(供裁剪)

- [ ] DESIGN §7 末尾增「段化观测清单(草案)」小节:上表六指标 + 两条纪律;声明「无新 workload 证据不重启三难讨论」维持原文
- [ ] (可选,另一批次)scripts/ 增压测脚本骨架(指标 1/3/5 的采集循环),或先用一次性 ad-hoc 脚本采首批数据
- **不做**:段化实现设计、/metrics 端点、指标采集进产品二进制——本项只回答「看什么」

---

## 5. 调研项 5:健康自检命令(kb doctor)

### 5.1 cas-kb 现状

- 自检能力分散在四个入口:`kb fsck`(全对象哈希 + 内部引用 + kind 一致性,「输出问题清单,发现问题时退出码非零——可直接接 CI 巡检」,DESIGN §6.5)、`kb version`(版本与平台)、`kb update`(GitHub 最新版检查)、`/healthz`(serve 进程内探活,返回 backend/schema_version/project)。
- 配置项八个(KB_DSN/KB_BRANCH/KB_GC_PROTECT/KB_REMOTE_DSN/KB_TEST_DSN/KB_PROJECT/KB_UPDATE_REPO/GITHUB_TOKEN,DESIGN §8.2);docs/serve.md 另有部署形态巡检锚点(绑定面、令牌文件权限 600 等,T43 演练验证过其可照做性)。
- 缺口:没有任何命令把「库完整性 + 版本 + 配置 + serve 可达性」一站拉通;运维要在多个命令间拼装。

### 5.2 社区通行做法与证据

- **brew doctor**(实抓 https://docs.brew.sh/Manpage ):「Check your system for potential problems. **Will exit with a non-zero status if any potential problems are found.**」;形态三要素:检查项可列举(`--list-checks`)、检查项可单独执行(「List all audit methods, **which can be run individually if provided as arguments**」)、警告语气克制(「these warnings are just used to help… If everything… is working fine: please don't worry」)。
- **git fsck**(实抓 https://git-scm.com/docs/git-fsck ):「Verifies the connectivity and validity of the objects in the database」;`--dangling` 默认打印「存在但从未被直接使用」的对象(**警告与错误分级输出的范本**:悬垂对象是信息不是错误);`--strict` 提严格档、`--connectivity-only` 跳过全对象哈希换速度。
- **gh doctor:不存在。**经两路核实(2026-09-02):cli/cli 主干文件树(git trees API 全量,1824 项,`pkg/cmd` 下 39 个子命令目录)无 doctor;提交历史按路径查询(`commits?path=pkg/cmd/doctor`)返回空数组。gh 的退出码契约另有专页(实抓 https://cli.github.com/manual/gh_help_exit-codes ):成功 0、失败 1、被取消 2、需认证 4,并提醒「特定命令可能有更多退出码,依赖退出码控制行为时应查该命令文档」。**结论:gh doctor 是社区以讹传讹的引用,本报告不采信其「输出风格」,改用 gh help exit-codes 的退出码契约作 gh 侧证据。**

### 5.3 对 cas-kb 的建议

**值得加,采纳 brew doctor 的形态骨架,内核直接复用 kb fsck;输出分级学 git fsck(错误/警告/信息三档),退出码学 gh(0/1 两档起步)。**

1. **命令形态草案**:

   ```
   kb doctor [--json] [--check <name>…] [-p 项目]
   ```

   - 无参数:跑全部检查,逐项输出「ok / warn / fail + 一句人话 + 可行动修复建议」,末尾汇总行如 `doctor: 5 ok, 1 warn, 1 fail`;**有 fail ⇒ 退出码 1,仅 warn ⇒ 退出码 0**(warn 不拦 CI,对齐 brew doctor「warning 只助调试」的克制);
   - `--check <name>`:单独跑指定检查(brew doctor 同款);`kb doctor --list-checks`(或 -l)列举全部检查名——检查名即契约,新增不破坏旧名;
   - `--json`:机器可读(`{check, status: ok|warn|fail, detail}` 数组),走 internal/view 同款契约纪律,供 AI/巡检脚本消费。
2. **检查项清单(v1 六项,全部复用现成能力,不写第二套诊断逻辑)**:

   | 检查名 | 内容 | 状态映射 |
   |---|---|---|
   | `storage` | 打开 KB_DSN 后端 + schema_version 门禁 | 打不开/版本不符 = fail |
   | `fsck` | 等价 `kb fsck`(全对象哈希 + 引用) | 问题 = fail;悬垂/未达对象 = warn(GC 可清,信息非错误,学 git fsck --dangling) |
   | `version` | `kb version` 本体版本;dev 构建提示不参与比较(沿 §8.4 口径) | 仅信息输出,永不 fail |
   | `config` | KB_* 环境变量逐个核对取值合法性(DSN 形态、KB_PROJECT 存在性等;**只报值是否可用,绝不回显连接串凭据段**,对齐 §8.2 凭据纪律) | 非法 = fail;未设置的默认项不提 |
   | `gc-protect` | KB_GC_PROTECT 开关态 + 分支表备份目录可写性 | 不可写 = warn |
   | `serve` | 若本机 127.0.0.1:8787 有实例,GET /healthz 验证 backend/schema 一致;无实例 = ok(明确「未运行」不是错) | 探活可达但 schema 不符 = warn;连接拒绝 = ok + 信息 |
3. **取舍记录**:不采纳「doctor 内嵌修复动作」(自动 gc/自动备份)——brew doctor 的克制哲学:doctor 只诊断不施治,修复永远指向可行动命令;不采纳退出码分级到 2/4(取消/认证场景不存在),v1 两档(0/1)起步,`--json` 内部细分。

### 5.4 落地改动清单草案(供裁剪)

- [ ] cmd/kb 增 `kb doctor`(纯组装:store.Open + repo.FSCK + 版本 + 配置核对 + 可选 /healthz 探活;新增诊断逻辑应为零)
- [ ] 检查项注册表(check name → 函数),`--list-checks` / `--check` / `--json` 三旗标;退出码 0/1 两档
- [ ] usage + DESIGN(建议 §6.5 后邻位置或 §8 新小节)+ CHANGELOG 三处同步;凭据不回显写入文档纪律
- [ ] e2e.sh 增 doctor 段(健康库全 ok 退出 0;人为制造悬垂对象验 warn 不拦退出;坏 DSN 验 fail 退出 1)
- **不做**:自动修复、服务模式守护、PG 后端专属深检(v1 六项已覆盖双后端公共面)

---

## 6. 实施清单(优先级排序,供负责人直接立项)

| 项 | 内容 | 优先级 | 预估规模 | 依赖 | 对应节 |
|---|---|---|---|---|---|
| T47-A | `GET /api/v1/merge-state`(独立端点 + 单枚举 + 派生布尔 + 冲突清单契约复用;idle=200,项目/分支不存在=404) | **高**(闭合 merge-design §4-9 开放问题;M5 合并闭环缺自动化观测面,Agent 裁决链路刚需) | 小-中(1 端点 + view 契约 + parity/错误语义测试 + 三处文档) | 无(M5 已交付);与 §8.5 端点表纪律同批 | §1 |
| T47-B | `kb doctor`(六检查项,纯组装零新增诊断;brew doctor 形态 + git fsck 分级 + 0/1 退出码) | **高**(运维价值/规模比最高,全部复用现成能力) | 小(1 命令 + 注册表 + e2e 段 + 三处文档) | 无(fsck/version/healthz 均已交付) | §5 |
| T47-C | drill 脚本固化(`scripts/drill-multi.sh` + `scripts/drill-serve.sh`,TAP 式汇总 + trap 清理 + 参数化 + 可选跳过) | **中**(T42/T43 暴露过缺陷,固化后 v0.6.1 修复才有自动回归;不进默认门禁) | 中(两脚本各约 150-250 行 + verify 接入 + README) | 无;建议排在 T47-A/T47-B 之后交付以一并覆盖其 e2e | §3 |
| T47-D | 冷启动指引文案(README 三步走 + pull 报错文案配代价说明 + 「冷启动完成」提示行) | **中**(旗标已交付,纯文案与提示行,可与任一批次捎带) | 小(文案 + 提示行 + e2e 冷启动段) | 无;若先落 T47-C 则由其复验 | §2 |
| T47-E | DESIGN §7 段化观测清单草案(六指标表 + 决策/护栏分类 + 纪律声明) | **低**(纯文档;数据未到触发线,段化不立项——清单的价值恰是「到了触发线时有据可查」) | 小(一节文档,可选配一个 ad-hoc 采集脚本) | 无 | §4 |

排序理由:T47-A/T47-B 都是「无依赖、小规模、闭合明确缺口」的即战力,且互不相干可并行;T47-C 优先级让位于功能项但在 T47-A/B 落地后立刻有价值(把新端点/新命令纳入剧本);T47-D 体量最小可捎带;T47-E 是「防遗忘」型文档投资,不急但建议随 v0.7 之前的文档批次一并落。

---

## 7. 证据与检索方法附注(诚实清单)

- 全部链接为 2026-09-02 实抓(curl 或等价 HTTP GET),关键断言尽量给「官方文档 + 线上实测」双证据(GitHub parents 用 torvalds/linux 实测、PR 状态机用 octocat/Hello-World#1 实测)。
- **检索不到/未采信的项,如实记录**:
  - `gh doctor`:当前 gh CLI 不存在(cli/cli 主干文件树与按路径提交历史双重核实为空);社区多处以讹传讹,本报告不引用其「输出风格」;
  - git 2.9.0 发行注记原文(kernel.org 与 raw.githubusercontent 两条路径均 404,未取得):未引用;`--allow-unrelated-histories` 的依据以 git-merge 手册原文为准(已实抓,足够权威);
  - GitLab `detailed_merge_status` 的完整取值枚举:API 文档页只取得「15.6 弃用 merge_status 改推 detailed_merge_status」与 `has_conflicts` 定义,取值全集未在本次抓取范围内核实,故正文不断言其取值集合;
  - GitHub `mergeable_state` 的完整取值集合:实测仅见 `dirty`,社区讨论(github.com/orgs/community/discussions/21886)未抓到正文,正文只断言「存在多值细化态」并引该讨论为参考。
- 本文件为 T47 唯一交付物;未修改任何代码与既有文档;不推送、不合并。
