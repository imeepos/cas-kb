# T42 真机落地演练报告:多端互写(drill/multi-cli)

- 日期:2026-09-02(本地 -07:00,05:35–05:45)
- 演练方式:headless 无人对话,真实进程 + 真实库文件,e2e 脚本之外的全手工操作
- 工作树:/Users/imeepos/ext512/ymm-001/cas-kb-t42,分支 `drill/multi-cli`,HEAD `e408e6d`,演练前工作树干净
- 构建:`go build -o /tmp/drill-t42/kb ./cmd/kb`(一次成功);本报告所有 kb 调用均指向该二进制
- 环境:darwin/arm64,go1.26.2;`KB_DSN` 指向独立临时 SQLite 库(/tmp/drill-t42/{a,b,c,d}.db),`KB_PROJECT=demo`;全部临时产物在 /tmp/drill-t42/(报告定稿后清理)
- 版本自证:`kb version` → `kb dev(darwin/arm64,go1.26.2)`(本地构建为 dev ✓)

## 结论总览

| 演练腿 | 剧本载体 | 结果 | 说明 |
|---|---|---|---|
| 腿 1 双库分叉合并 | A/B 库 | **BLOCKED**(D1、D2) | 剧本第一步「A pull B 应 fast-forward」报错;核心「A pull --merge 零冲突落库」被「两库无共同历史」拒绝,后续断言连锁不可达 |
| 腿 1 补充实验(同源路径) | C/D 库 | PASS | 证明分叉合并核心能力可用,断点在「无共同历史」前置判定,详见 D2 |
| 腿 2 真实冲突裁决 | C/D 库(承载偏差) | PASS | 冻结、--stage 升格、--continue 双亲、--abort 回滚全部符合预期;剧本指定 A/B 因 D2 无法进入冲突中间态,故以同源库对 C/D 承载 |
| 腿 3 备份恢复与版本 | C 库(含合并历史) | PASS | backup→wipe→restore→fsck 全通过,笔记与双亲合并历史逐行还原;Release v0.6.0 资产外网验证存在 |

**缺陷 2 项(D1、D2),无修复动作,未改任何代码与既有文档。**

---

## 腿 1:双库分叉合并(核心)—— BLOCKED

### 步骤与实际输出

1. `kb init` 初始化 A(/tmp/drill-t42/a.db)、B(/tmp/drill-t42/b.db)→ 均成功(schema v5);`kb project create demo --desc …` 两侧均成功。
2. A 侧写入 3 条嵌套笔记(go/concurrency/channel、go/concurrency/goroutine、go/concurrency/context)→ 均成功;A 的 main = `sha256:e12296de4`(3 提交,根 parent=none)。
3. **A pull B(预期 fast-forward)→ FAIL(D1)**
```
$ kb pull /tmp/drill-t42/b.db
kb: repo: 远端分支 "main": store: 分支不存在        [exit=1]
```
   取证:`project create` 只写 projects 表,B 的 branches 表为空(`kb log` 显示 "(no commits)");A 侧无副作用(main 不变)。
   对照(补充实验):空库 D pull 非空库 C → `已同步 12 个对象(fast-forward)` 正常。行为不对称:「本地空」可 ff,「远端空」硬报错。
4. B 侧写入 2 条不同路径笔记(python/decorator、rust/lifetime)→ 成功;B 的 main = `sha256:060ec7cb6`(2 提交,根 parent=none)。
5. **A、B 互 pull(无参,验证旧语义分叉拒绝)→ PASS**
```
kb: pull: repo: 本地与远端已分叉,拒绝快进,需要 --force 才能覆盖;或改用 kb pull --merge 做三方合并   [exit=1]
```
   双向文案一致,均含 --merge 指引 ✓;拒绝后 A main=e12296de4、B main=060ec7cb6 均未动 ✓。
6. **A pull --merge(预期零冲突落库)→ FAIL(D2)**
```
$ kb pull /tmp/drill-t42/b.db --merge
kb: repo: 两库无共同历史,无法三方合并(确认两端同源,或改用 --force 覆盖)   [exit=1]
```
   连锁断言全部不可达:kb log 无合并行、`kb note ls` 仍 3 条(B 侧 2 条未并入)、`kb search 装饰器/悬垂` 无结果(跨侧内容未进来)、B pull A 仍分叉拒绝、B 第二次 pull 的「已是最新」无法验证。
   注意矛盾:第 5 步文案指路「改用 --merge」,而 --merge 对此场景(无共同历史)拒绝,指引断裂。
7. **腿 1 判定:BLOCKED**(剧本核心断言依赖 D1/D2 消除;按铁律只记录、不修复、不改剧本,继续其余腿)

### 补充实验:C/D 同源路径(定位 D2 影响面,非修复)

目的:区分「剧本顺序无法建立同源」与「分叉合并能力缺失」。在独立 C/D 库上重走剧本:

1. C seed 1 条(base/first)→ D pull C → `已同步 12 个对象(fast-forward)` ✓(空库拉非空库 ff 正常,反证 D1)
2. C、D 分叉各写 1 条 → **C pull D --merge → 零冲突落库** ✓:
```
已同步 11 个对象(merge)
自动合并 2 条;冲突 0 条
合并快照 sha256:c9c4d142…(parents sha256:22911d3ea sha256:5e84254b3)
```
   - kb log 合并行显示双亲:`parent=sha256:22911d3ea,sha256:5e84254b3  merge theirs` ✓
   - `kb note ls` 两侧笔记齐全(3 条)✓;`kb fsck` 40 对象完整 ✓
   - `kb search decorator --snippet` 跨侧命中 D 侧 python/decorator,片段 `D 侧 【decorator】 笔记`(命中高亮)✓
3. D pull C → `已同步 17 个对象(fast-forward)` 至合并快照 ✓
4. **D pull C 第二次 → `已是最新`(空操作)✓ —— §1.1 修复真机复验通过**

结论:同源前提下的分叉→拒绝→合并→ff→幂等 no-op 全链可用;腿 1 剧本断言的能力本体存在,断点仅在「无共同历史」这一前置(见 D2)。

---

## 腿 2:真实冲突裁决 —— PASS(承载库对 C/D)

> 承载偏差说明:剧本写「两库再次各写同一路径」,隐含沿用腿 1 的 A/B;因 D2,A/B 无法进入合并中间态(前置即拒绝)。改以同源库对 C/D 执行剧本的每一个动作,证据如下。

### 冲突制造与检出

- C、D 各写同一路径 `shared/decision` 不同内容(C 版快照 ffa02346e;D 版快照 3f13b04a7)。
- 记录合并前 C 头:main = `sha256:ffa02346e`。
- **C pull D --merge → 退出非零 + 冲突清单 ✓**:
```
已同步 16 个对象(merge)
分叉:base sha256:c9c4d142c  ours sha256:ffa02346e  theirs sha256:3f13b04a7
自动合并 0 条;冲突 1 条:
  shared/decision  content  base   ours sha256:728e9829f  theirs sha256:19378ec62
已建中间态分支 main-merge:逐条 kb note set/rm <路径> --stage 裁决后 kb merge --continue;或 kb merge --abort 放弃
kb: pull: 合并检出冲突,未落库                                                    [exit=1]
```
- **main 指针不动(C 侧核对)**:branch ls 仍 `main sha256:ffa02346e…`(与 pull 前一致)✓;中间态分支 `main-merge` 建立 ✓。

### 冻结与裁决

- **中间态下直接写被拒绝且提示可行动 ✓**:
```
$ kb note set shared/decision --title 绕过裁决 …
kb: repo: note set 被拒绝:分支 "main" 存在未完成合并(kb note set/rm <路径> --stage 裁决后 kb merge --continue 收束,或 kb merge --abort 放弃)   [exit=1]
```
- **`kb note set --stage` 升格为裁决动作 ✓**:`staged shared/decision -> sha256:c8af31fc…`
- **`kb merge --continue` 落双亲快照并清理中间态 ✓**:
```
合并完成:1 条裁决;快照 sha256:eb0d7101…(parents sha256:ffa02346e sha256:3f13b04a7)
```
  kb log 合并行双亲 ✓(`parent=sha256:3f13b04a7,sha256:ffa02346e  resolve shared/decision: 采纳融合语义`);branch ls 只剩 main(main-merge 消失)✓;note ls 中 shared/decision 为裁决版(「最终裁决」)✓;fsck 94 对象完整 ✓。

### --abort 腿

- D 先 ff 至 eb0d71019;两侧再各写 `shared/abort-test` 不同内容(C:77ff09cb7;D:6cbe8aeb3)。
- C pull D --merge → 冲突中间态再次建立(main-merge = ae9491bd9)。
- **`kb merge --abort` → 回到合并前,无残留 ✓**:
```
已放弃合并(丢弃 0 条裁决),回到合并前
```
  main 保持 77ff09cb7(与合并前一致)✓;main-merge 消失 ✓;kb log 与合并前逐行一致 ✓;note ls 中 shared/abort-test 为 C 版(ours 保留)✓;fsck 120 对象完整 ✓。

---

## 腿 3:备份恢复与版本 —— PASS(承载库 C,含双亲合并历史)

1. `kb backup /tmp/drill-t42/c-backup.ckb` → `备份完成`,对象 120 · 项目 2 · 分支 1(文件 72,169 字节)✓
2. `kb wipe --force` → `已清空并重置为全新库(schema v5)`;note ls=(no notes)、log=(no commits)✓
3. `kb restore /tmp/drill-t42/c-backup.ckb` → `恢复完成: 对象 120 · 项目 2 · 分支 1` ✓
4. `kb fsck` → 检查 120 个对象,完整,无问题 ✓
5. 还原一致性断言:note ls 5 条与恢复前一致 ✓;kb log 6 行与恢复前逐行一致,两个双亲行(c9c4d142 merge theirs、eb0d7101 resolve …)均在 ✓;branch ls main=77ff09cb7、项目描述「同源实验C」还原 ✓
6. 版本验证:
   - `kb version` → `kb dev(darwin/arm64,go1.26.2)`,本地构建为 dev ✓
   - 外网可用,无 SKIP-NO-NET:`kb update` → `最新版本: v0.6.0(2026-09-02 发布)`,平台产物 kb-0.6.0-darwin-arm64.tar.gz ✓
   - GitHub API 交叉验证(`GET /repos/imeepos/cas-kb/releases/tags/v0.6.0`,HTTP 200):tag v0.6.0,2026-09-02T12:18:15Z 发布,资产 6 项 —— kb-0.6.0-{darwin-amd64,darwin-arm64,linux-amd64,linux-arm64,windows-amd64} 与 sha256sums.txt ✓(发布产物存在)

---

## 缺陷清单

### D1:pull 对「远端无分支」(空远端)硬报错,而非 fast-forward/空操作

- **环境**:本报告演练环境;A/B 库均 `kb init` + `project create demo --desc`,A 有 3 提交,B 零提交
- **步骤**:`KB_DSN=a.db KB_PROJECT=demo kb pull /tmp/drill-t42/b.db`
- **复现概率**:100%(确定性:`project create` 不建 branches 条目,首个提交才建 main;远端项目零提交即必现)
- **预期**:演练脚本规定「A pull B(此时 B 空,应 fast-forward)」;对照同构建「空库 D pull 非空 C → fast-forward 成功」,合理语义为远端空 → 视为已是最新/无害空操作
- **实际**:`kb: repo: 远端分支 "main": store: 分支不存在`,exit=1,A 侧无副作用
- **影响面**:双库冷启动场景(腿 1 剧本第一步)失败;且因本次 pull 未发生,两库始终无共同历史,直接触发 D2,腿 1 核心合并不可达。另注:即便 D1 修为空操作,若不引入共同基线,D2 仍会阻断剧本路径(两缺陷需一并评估)。

### D2:两库无共同历史时 `pull --merge` 拒绝,与剧本「零冲突落库」预期不符,且与自身文案指引自相矛盾

- **环境**:本报告演练环境;A/B 各自独立提交(A 3 条、B 2 条,根快照 parent=none 且互不相同)
- **步骤**:`KB_DSN=a.db KB_PROJECT=demo kb pull /tmp/drill-t42/b.db --merge`
- **复现概率**:100%(确定性:两链无公共祖先即必现)
- **预期**:演练脚本规定「A 执行 kb pull --merge → 应零冲突落库」(即空基线三方合并,两侧新增互不冲突应全取)
- **实际**:`kb: repo: 两库无共同历史,无法三方合并(确认两端同源,或改用 --force 覆盖)`,exit=1,未落库
- **矛盾证据**:同场景无参 pull 的分叉报错文案为「…或改用 kb pull --merge 做三方合并」——被指引的路走不通
- **影响面**:「两端各自生长、事后合并」的多端互写故事在首次同步时中断;仅 --force(丢弃一方)可用。同源分叉合并本体正常(见腿 1 补充实验:零冲突双亲、跨侧 search、ff、幂等 no-op 全通过),缺陷局限于 merge-base 缺失时的空基线合并策略
- **文案小瑕疵(随 D2 一并记录)**:「已分叉」判定未区分「真分叉(有共同祖先)」与「无共同历史」,前者才应给 --merge 指引

---

## 交付与留痕

- 本报告为本次演练唯一新增文件;未修改任何代码与既有文档;临时产物(/tmp/drill-t42/:二进制 kb、a/b/c/d.db、c-backup.ckb、commit-msg)在提交后清理
- 提交:docs(review),分支 drill/multi-cli

VERDICT: PASS-WITH-DEFECTS
