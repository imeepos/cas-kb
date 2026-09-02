# drill-serve:docs/serve.md 真机运维演练报告(T43)

- 日期:2026-09-02(会话 T43,headless 演练;验证者不修复,仅记录)
- 工作树:/Users/imeepos/ext512/ymm-001/cas-kb-t43 @ `drill/serve-ops` @ e408e6d(演练全程无本地改动,工作树干净)
- 被测构建:`go build -o /tmp/drill-t43/kb ./cmd/kb` → `kb dev(darwin/arm64,go1.26.2)`
- 准绳文档:docs/serve.md @ e408e6d(端点契约另以 DESIGN §8.5/§8.6 为权威)
- 环境:macOS(darwin/arm64);端口统一 127.0.0.1:18787;临时库 `/tmp/drill-t43/ops.db`(schema v5,SQLite);临时产物限 /tmp/drill-t43/,报告交付后清理
- 本机工具面:ss 缺失、systemd-analyze 缺失(macOS 预期,文档 §1/§5.1 本就提供了 lsof 替代与 Linux 专属命令);lsof/openssl/plutil/xmllint/sqlite3 可用

## 结论速览

| 腿 | 内容 | 结论 |
|---|---|---|
| 1 | 无令牌只读基线 | **PASS**(§1 默认绑定、§2.4 第 1 行、横幅自检文案、监听面全部兑现) |
| 2 | 令牌写入闭环 | **PASS**(§2.1 存放、§2.4 第 2–4 行、201 契约、写入即时可见、CLI 读一致) |
| 3 | 文档承诺核验 | **PASS**(§2.1 ps 警告属实、§6.3 日志核验通过、§3 锁忙 503 机制复现) |
| 4 | 文档准确性 | **BLOCKED(launchd 子项)**——systemd/§7 子项已完成;缺陷 D1 见下 |

**VERDICT: PASS-WITH-DEFECTS**

---

## 腿 1 无令牌只读基线

### 1-A 默认绑定探测(无 --addr,§1 承诺「默认只绑回环 127.0.0.1:8787」)

- 启动横幅(全文):
  ```
  kb serve 只读 HTTP API(未配置写入令牌,纯只读)
  后端 sqlite(/tmp/drill-t43/ops.db)
  项目作用域 default(分支 main)
  监听 http://127.0.0.1:8787
  Ctrl-C 优雅退出
  ```
- `GET /healthz` → 200;`lsof -nP -iTCP -sTCP:LISTEN` 仅见 `127.0.0.1:8787 (LISTEN)`,无 `0.0.0.0`/`::`/`*` 监听 → §1 巡检锚点通过
- `kill -TERM` 后进程 1.2s 内退出(§1 优雅退出承诺成立;排空窗口未单独计时)

### 1-B 主实例 127.0.0.1:18787(无令牌)

| 检查项 | 预期(文档) | 实际 | 判定 |
|---|---|---|---|
| 横幅首行 vs §2.4 自检文案 | `kb serve 只读 HTTP API(未配置写入令牌,纯只读)` | 逐字一致(grep 全行精确匹配命中) | ✅ |
| GET /healthz | 200 可用 | 200 `{"ok":true,"backend":"sqlite","schema_version":5,"project":"default"}` | ✅ |
| GET /api/v1/projects、/tree、/search、/log | 无鉴权照常可用 | 全部 200 | ✅ |
| GET /api/v1/note?path=hello(空库) | 404 | 404 | ✅ |
| POST /api/v1/note(无令牌) | 403 + §2.4 文案 | 403,error 字段逐字 =「服务未配置写入令牌,当前为只读模式;设置 KB_SERVE_TOKEN 后启用」 | ✅ |
| DELETE /api/v1/note(无令牌) | 403 | 403,同上文案 | ✅ |
| POST 到只读端点(/api/v1/search) | —(附加) | 405 `{"error":"只读 API:仅支持 GET,收到 POST"}` | ✅ |
| 监听面(§1 锚点) | 仅 127.0.0.1 | lsof 仅 `kb … 127.0.0.1:18787 (LISTEN)`,无跨界面 | ✅ |

> 记录:403 响应体为 2 空格缩进 JSON + 末尾换行(error 字符串与文档逐字一致);§2.4 示例以紧凑单行表意。格式差异记入偏差清单 P1,行为无偏差。

## 腿 2 令牌写入闭环(§2.1 + §2.4 第 2–4 行)

### 令牌生成与存放(§2.1)

- `openssl rand -hex 32` → 64 个 hex 字符(≥32 字节随机,非口令派生)✅
- `umask 077` 写入 `/tmp/drill-t43/serve.env`:`stat` 权限 **600**、属主当前用户 ✅
- 注入方式:按 §5.2 同款 `set -a; . serve.env; set +a` 环境变量注入重启(非 --token 旗标)✅

### 鉴权矩阵与写入(§2.4 第 2–4 行)

| 请求 | 预期 | 实际 | 判定 |
|---|---|---|---|
| 无头 POST | 401 | 401 `{"error":"缺少写入令牌:请求需带 Authorization: Bearer <token> 头"}` | ✅ |
| 错令牌 POST(`Bearer deadbeef`) | 401(不回显) | 401 `{"error":"写入令牌无效(Authorization: Bearer <token>)"}`;响应体含真令牌次数 = 0 | ✅ |
| 正确 Bearer POST | 201 + path/address/short | 201 `{"path":"demo/hello","address":"sha256:1ff8…","short":"sha256:ab335872a"}` 三字段齐备 | ✅ |
| 读端点(配置令牌后,无头) | 无鉴权照常可用 | /healthz、/tree 均 200 | ✅ |

### 写入即时可见 + CLI 一致(§3「不存在第二套写行为」的读侧证据)

- GET /api/v1/note?path=demo/hello → 200,title/tags/body 与写入一致
- GET /api/v1/search?q=协程 → 命中 demo/hello(score 0.2877…)
- GET /api/v1/log?limit=1 → 快照 id `sha256:ab335872a` = POST 响应的 `short` ✅
- CLI 读同库(`KB_DSN=/tmp/drill-t43/ops.db`):
  - `kb note get demo/hello`:path/addr/title/tags/body 与 API 响应逐字段一致 ✅
  - `kb search 协程 --json` 输出与 API /search JSON 完全一致(含相同 score)✅
  - `kb log` 首列 = API `short` ✅
  - `kb fsck` →「完整,无问题」✅
- 附加闭环:正确令牌 DELETE → 200 `{"removed":1,"short":"sha256:6d3463ef5"}`;删除后 GET → 404 ✅

## 腿 3 文档承诺核验

### 3-a §2.1 ps 可见性警告(--token 旗标实例)

- 以 `kb serve --addr 127.0.0.1:18787 --token <T1>` 起实例,`ps -Ao command` 可见:
  ```
  /tmp/drill-t43/kb serve --addr 127.0.0.1:18787 --token <64hex已打码>
  ```
  → **T1 确实出现在命令行,证实 §2.1「命令行参数对同机用户经 ps 可见」的警告属实** ✅
- 同实例同时注入环境令牌 T2(serve.env 中的值):POST 带 T1 → **201**;POST 带 T2 → **401** → §2「--token 旗标优先于环境变量 KB_SERVE_TOKEN」语义成立 ✅
- 附注(方法论):核验脚本里 `ps | grep -q -- "--token $T1"` 会把 grep 自身 argv 算进 ps 输出(自匹配);本报告采信的是上屏 ps 行的直接屏显证据,不受该伪影影响。巡检时建议 `ps -Ao command | grep '[k]b serve'` 类方式规避。
- 平台注意(记偏差 P2):文档 §6.1/§6.3 的 `pgrep -af 'kb serve'` 在 macOS 上只输出 PID、不输出命令行,无法完成 §6.3「看 ps 命令列有无令牌」的核验意图;Linux(procps)无此问题。

### 3-b §6.3 日志核验(令牌不写日志、不回显)

- 文档原样方法:`grep -cFf <(awk -F= '/^KB_SERVE_TOKEN=/{print $2}' serve.env) <日志>` 对环境注入实例日志(serve-w.log)→ 计数 **0** ✅
- 旗标实例日志(serve-flag.log)分别 grep T1/T2 → 均为 **0** 行 ✅(令牌即便走旗标,也不落日志)
- 401/403/503 响应体不含令牌值(腿 2/腿 3 各响应已 grep,均 0)✅
- §6.3「环境文件权限确为 600」已验(腿 2 stat);「未纳入任何版本库」:serve.env 位于 /tmp,git status 全程干净 ✅

### 3-c §3 锁忙 503 机制

- 密集并发写(serve API 40 路 POST × CLI 40 路 `kb note set` 同时跑):API **40×201**、CLI **0 失败**、fsck 完整 —— SQLite busy_timeout(10s,经 DSN pragma 注入,与 §6.2 表格「先等待、超时即锁忙」一致)把常规竞争全部吸收,未出现 503。按演练说明记为实际行为,不算缺陷。
- 确定性复现(外部进程持 `BEGIN EXCLUSIVE` 15s,制造超时锁忙):
  - 正确令牌 POST → **等待 10.195s 后 503**,响应:
    ```
    {"error":"知识库正被其他写入占用(serve 与 CLI 同时写);请稍后重试或改用 CLI: store: Put 失败: database is locked (5) (SQLITE_BUSY)"}
    ```
    与 §3 承诺的 503 + 可行动文案逐字吻合(冒号后附底层 SQLITE_BUSY 详情)✅
  - 持锁期间 GET /api/v1/note → **200**、GET /healthz → 200(§2.4「读端点不受影响」)✅
  - 持锁期间 CLI 写 → 直接报错退出 1:`kb: store: 迁移 DDL 失败: database is locked (5) (SQLITE_BUSY)`(§3「CLI 侧遇锁忙直接报错」成立;报错点在启动迁移 DDL 的写锁,细节与 §6.2 模型一致)✅
  - 锁释放后重试同一 POST → **201**(503 作为可重试信号的闭环)✅
  - 全程 `kb fsck` 三次均「完整,无问题」——「失败只留未达对象交 GC,不产生半写状态」未见反例 ✅
- 演练执行记录:3-c 首轮因脚本忘注入环境令牌(serve 以纯只读横幅起,API 侧全 403)作废重跑;属演练脚本失误,非产品行为。

## 腿 4 文档准确性

### 4-A systemd 示例(§5.1)——静态核验,本机无 systemd-analyze(已注明)

- 提取 ini 块后逐项检查:[Unit]/[Service]/[Install] 三段齐备;Description/After/User/Group/EnvironmentFile/ExecStart/Restart/TimeoutStopSec/WantedBy 全部存在;ExecStart 显式 `--addr 127.0.0.1:8787`(与 §5「两处示例都刻意只绑回环」一致);TimeoutStopSec=15 ≥ 5s 排空窗口,自洽。
- **无法做 systemd-analyze verify**(macOS 无 systemd;亦未安装任何服务)——按演练说明注明。语法级检查通过,但「verify 级」结论不可得。
- 静态注意点(记偏差 P3):unit 声明 `User=caskb/Group=caskb`,而 §5.1 的落地命令只有 `install -d -o caskb -g caskb …`,**未包含创建 caskb 用户/组的步骤**;照做会在 useradd 缺失的主机上失败(静态判定,未实机验证)。

### 4-B launchd 示例(§5.2)——plutil -lint + xmllint 实测:**失败,缺陷 D1**

- 提取 xml 块为 plist 文件后:
  - `plutil -lint` → 退出码 1:`Encountered unknown ampersand-escape sequence at line 10`
  - `xmllint` → 退出码 1:`parser error : xmlParseEntityRef: no name`,指向第 10 行(即 docs/serve.md 第 149 行):
    ```
    <string>. "$HOME/.config/caskb/serve.env" && exec /usr/local/bin/kb serve --addr 127.0.0.1:8787</string>
    ```
- 判定:**D1**——示例 plist 非法(XML 文本中裸 `&&` 未转义为 `&amp;&amp;`),照抄保存后 `launchctl bootstrap` 无法加载。本子项按铁律不修复,标记 **BLOCKED**。

### 4-C §6.1/§6.3 命令可照做性(顺带取证)

- §6.1 端口占用:占用 18787 后再起,退出码 1,报错 `kb: serve: 监听 127.0.0.1:18787 失败: listen tcp 127.0.0.1:18787: bind: address already in use` —— 与 §6.1 引述前缀逐字一致(文档示例省略了底层 cause,无碍)✅
- §6.1 `lsof -nP -iTCP:8787 -sTCP:LISTEN`、`ss …`:lsof 形态本机可用;ss 为 Linux 命令,文档已双列注明 ✅
- §6.3 `pgrep -af` 在 macOS 只回 PID(偏差 P2,见腿 3-a)⚠️

### 4-D §7 安全清单逐项标注

| §7 条目 | 本机可验性 | 本机证据 |
|---|---|---|
| 绑定面(仅 127.0.0.1,无 0.0.0.0/::) | **本机可验,已验 ✅** | 腿 1 lsof:默认与 18787 实例均仅回环监听 |
| 公网隔离(无公网直达) | **不可验(需目标环境)**(防火墙/隧道/代理拓扑不在本机) | 本机可验部分:监听面无公网绑定已证 |
| 令牌强度(openssl rand -hex 32,非口令/复用) | **本机可验,已验 ✅** | 64 hex 随机值,本次新生成、未复用 |
| 令牌存放(600 环境文件,不进命令行/日志/历史/版本库) | **本机可验,已验 ✅** | stat 600;环境注入下 ps 无令牌;三类日志 grep=0;/tmp 文件不入库(git 干净);演练 shell 非交互不落历史 |
| 令牌分发范围(名单化台账) | **不可验(需目标环境台账)** | 无部署台账可查 |
| 只读优先(无写入需求不配令牌) | **本机可验,已验 ✅** | 腿 1:无令牌实例写端点 403 锁死 API 写 |
| 备份频率(周期+关键动作前) | **周期合规不可验(需目标环境)**;命令可用性本机已验 | `kb backup` 冒烟:导出 .ckb(对象 531,约 1.1MB)成功 |
| fsck 巡检周期 | **周期合规不可验(需目标环境)**;命令可用性本机已验 | 演练内 `kb fsck` 多次运行均「完整,无问题」 |

## 缺陷清单

### D1(腿 4,launchd 子项 BLOCKED):§5.2 示例 plist 非法,照做即失败

- **现象**:`plutil -lint` 与 `xmllint` 均拒绝 §5.2 提取出的 plist;launchd 无法加载。
- **复现**:将 docs/serve.md §5.2 ```xml 代码块存为 .plist → `plutil -lint <文件>` / `xmllint <文件>`。
- **预期**(文档承诺):示例「可照做」,加载即用;§5 开头称「令牌都经环境文件引用,不硬编码进 unit/plist 本体」之外的格式应当合法可载。
- **实际**:第 149 行 `. "…" && exec …` 中裸 `&&` 违反 XML 实体规则(xmlParseEntityRef: no name / unknown ampersand-escape sequence)。
- **定位**:docs/serve.md §5.2,plist 第 10 个内容行(文档第 149 行),`ProgramArguments` 的 sh -c 字符串。
- **修复建议(未执行,验证者不修复)**:改为 `&amp;&amp;`,或把 sh -c 字符串拆进独立包装脚本以避开 XML 转义。
- **影响面**:仅文档示例;serve 本体行为无涉(本演练其余各腿证明服务行为与文档一致)。

## 文档偏差清单(含无害差异)

- **P1**:§2.4/§3 的响应示例以紧凑单行 JSON 表意;实际响应体为 2 空格缩进多行 + 末尾换行(与 CLI printJSON 同款)。error 文本逐字一致,语义零偏差,纯排版差异。
- **P2**:§6.1/§6.3 的巡检命令 `pgrep -af 'kb serve'` 在 macOS(BSD pgrep)只输出 PID、不输出命令行;§6.3 以该命令核验「ps 命令列看不到令牌」在 macOS 上达不到意图(看不到一切命令行,核验形同虚设),需改用 `ps -Ao command`。Linux 无此问题;建议文档补一句平台差异。
- **P3**:§5.1 systemd 落地命令未包含创建 `caskb` 用户/组的步骤,而 unit 与 `install -o caskb -g caskb` 都依赖该用户存在;照做的运维在无此用户的 Linux 主机上会卡住(静态判定)。
- **P4**:§6.3「`grep -cFf <(awk …) <(journalctl …)`」期望输出 0 的写法在 grep 无匹配时退出码为 1(计数仍为 0);照做者若以退出码判定会误判失败。文档注释已写「期望输出 0」,语义正确,仅提示以数值而非退出码判定。
- **P5(演练方法侧,非文档)**:macOS 下 `cat -A` 不可用(BSD cat 无 -A),核验脚本首跑该诊断行无输出;与被测产品无关,仅存档。

## 清理记录

- 演练内全部 serve 实例经 SIGTERM 退出并逐一确认(默认探测、只读基线、旗标实例、写入型×2);
- 端口 18787 释放,无残留监听;临时目录 /tmp/drill-t43/(含 serve.env 令牌、ops.db、日志)于报告提交后整体删除。

VERDICT: PASS-WITH-DEFECTS
