# kb serve 运维安全指南

面向运维与部署的 `kb serve` 操作手册:部署形态、令牌管理、与 CLI 共存、备份与维护窗口、服务化示例、常见问题与安全清单。
端点契约与设计语义以 [DESIGN.md](../DESIGN.md) §8.5/§8.6 为权威;旗标与环境变量以 `cmd/kb/serve.go` 与 `kb --help` 为准(本文基于库 schema v5,写 API 为 M4.1 形态)。
本文所有令牌均为占位写法,真实令牌只存在于权限 600 的环境文件中,任何文档与配置模板不得出现真实值。

## 1. 部署形态

`kb serve [--addr 127.0.0.1:8787] [--token <值>]` 默认只绑回环(`127.0.0.1:8787`),启动横幅打印当前模式(纯只读/写入型)、后端、项目作用域与监听地址;SIGINT/SIGTERM 优雅退出(停收新请求、默认 5s 排空在途请求)。

| 形态 | 绑定 | 适用 | 主要风险 | 加固要求 |
|---|---|---|---|---|
| 本机专用(默认) | `kb serve`(即 `127.0.0.1:8787`) | 本机 AI/Agent、编辑器插件、脚本免 shell 消费;单用户工作站或受控主机 | **读端点无鉴权**:同机其他本地用户/进程可读全库(读无鉴权是本机约定,不是缺陷,别在多用户机器上当成私有) | 多用户主机上只放无敏感内容的库,或收窄为专用用户运行 |
| 内网服务 | `kb serve --addr 192.168.x.x:8787` 或 `--addr 0.0.0.0:8787` 显式绑定 | 团队/多机消费知识库 | 内网任意可达者可读(读端点无鉴权);写端点仅一把静态令牌;无 TLS | 只绑内网网段;前置防火墙限制来源;要 TLS/账号体系就放加鉴权的反向代理后面 |

**不要直接暴露公网。** 读端点无鉴权、服务无 TLS、写令牌是单把静态口令、无速率限制——公网直达等于全库对互联网可读,写端点直接面对枚举与爆破。跨机消费的两条正路(与 DESIGN §8.5 一致):

- SSH 端口转发:`ssh -L 8787:127.0.0.1:8787 <主机>`,远端 `kb serve` 保持只绑回环;
- 反向代理:代理层负责 TLS 与鉴权,再转发到 `127.0.0.1:8787`,serve 仍不直接对外。

巡检锚点:`ss -ltnp`(Linux)/ `lsof -nP -iTCP -sTCP:LISTEN`(macOS)确认监听面与预期一致;无跨机需求时不应出现 `0.0.0.0`/`::` 监听。

## 2. 令牌管理

语义回顾(§8.6,以代码为准):`--token <值>` 旗标优先于环境变量 `KB_SERVE_TOKEN`;令牌只在进程内存中与请求头 `Authorization: Bearer <token>` 常量时间比较(`crypto/subtle`),**不写日志、不回显**;配置令牌后读端点保持无鉴权不变;未配置令牌时写端点一律 403(纯只读降级,安全底线)。

### 2.1 生成与存放

```bash
# ≥32 字节随机,非口令派生
openssl rand -hex 32
# 写入环境文件(属主=服务用户,权限 600,不入版本库)
umask 077 && echo "KB_SERVE_TOKEN=<上一步输出>" >> /etc/caskb/serve.env
```

**推荐用环境变量注入,而不是 `--token` 旗标**:命令行参数对同机用户经 `ps` 可见、且会进 shell 历史;环境文件 + 服务管理器注入则不出现在进程命令行上(见 §6.3 核验方法)。

### 2.2 轮换流程

令牌驻留进程内存,**无热轮换**——改环境变量必须重启 serve 才生效:

1. 生成新令牌(`openssl rand -hex 32`),更新环境文件中的 `KB_SERVE_TOKEN`;
2. 重启服务:`systemctl restart caskb-serve`(或 `launchctl kickstart -k gui/$UID/com.caskb.serve`);重启走 SIGTERM,停收新请求、5s 排空在途写;
3. 更新消费方(Agent/脚本)持有的令牌;旧进程退出即旧令牌失效,消费方收到 401 就是轮换未完成的信号。

泄露处置:立即轮换;随后用 `kb log` 与 `kb diff` 抽查该时间窗的快照链有无异常写入。

### 2.3 最小权限约定

- **令牌 = 全库写权限**:持有者可对 serve 作用域内任意项目/分支执行等价 `kb note set/rm` 的写入(写端点恰好两个,不暴露 stage/bulk 等其他写命令);serve 无账号体系、无按项目分权,一把令牌就是一把全库写钥匙。
- 分发名单化:每个部署单元/每个 Agent 独立一把令牌,登记「谁持有、跑在哪个进程」;离场/下线即轮换。
- serve 默认不记请求日志,写操作只能靠快照链(`kb log`)回溯——分发范围务必收小。
- 保护「读」不靠令牌(读端点永远无鉴权),靠网络边界:绑定面、防火墙、隧道(§1)。

### 2.4 无令牌 = 纯只读的降级行为表

实际响应体为 2 空格缩进 JSON + 换行;下表示例以紧凑单行表意,error 文案逐字一致。

| 状态 | POST/DELETE /api/v1/note | 读端点(/healthz、/api/v1/* GET) |
|---|---|---|
| 未配置令牌(空 `KB_SERVE_TOKEN`) | `403` + `{"error":"服务未配置写入令牌,当前为只读模式;设置 KB_SERVE_TOKEN 后启用"}` | 无鉴权,照常可用 |
| 已配置令牌,请求缺 `Authorization` 头 | `401` | 无鉴权,照常可用 |
| 已配置令牌,令牌错误 | `401`(响应不回显令牌) | 无鉴权,照常可用 |
| 已配置令牌,令牌正确,后端锁忙 | `503` + 可行动提示(§3) | 不受影响 |

运维自检:启动横幅首行即模式声明——「kb serve 只读 HTTP API(未配置写入令牌,纯只读)」或「kb serve 写入型 HTTP API(已配置写入令牌,写端点需 Bearer 鉴权)」,巡检时核对它与台账一致。

## 3. 与 CLI 共存

同一库有两条写路径:CLI(`kb note set/rm` 等)与 serve 写端点。两者复用同一套写逻辑(`repo.SetNote/RemoveNote`),语义逐字段一致,不存在「第二套写行为」。

**锁忙 503 语义**:serve 与 CLI 同时写依赖后端事务串行化(SQLite 单写者 + busy_timeout;PG 行锁)。后端报锁忙(`SQLITE_BUSY` / PG lock 类错误,`store.IsLockBusy` 统一识别)时,写端点返回:

```
503 {"error":"知识库正被其他写入占用(serve 与 CLI 同时写);请稍后重试或改用 CLI: …"}
```

调用方把 503 当可重试信号即可:写路径对象幂等,失败只留未达对象交 GC,**不产生半写状态**;响应 2xx 即全链路完成(blob/note/tree/snapshot + 检索索引增量 + 分支指针推进),fsck 恒可过、检索立即可见。客户端建议对 503 做指数退避重试;CLI 侧遇锁忙直接报错,人工错峰重试。

**单写者纪律(建议)**:同一台机器上,要么所有写都走 CLI,要么所有写都走 serve API,不混用。混用是允许的(有 503 保护兜底),但会把重试负担推给调用方。落地手段:

- 「人/脚本走 CLI」的机器:serve **不配令牌**(写端点 403,天然锁死 API 写,单写者自动成立);
- 「Agent/自动化走 API」的机器:约定该机不再执行 CLI 写命令,只保留 `serve` + 读类 CLI(`log/diff/fsck`)。

读无锁冲突:读端点与任何写入并发安全,可放心共享同一实例。

## 4. 备份与维护窗口

- **备份**:`kb backup [文件]` 导出整库为 `.ckb`(JSONL + 逐对象哈希,跨后端可移植;全库语义,不受 `-p`/`KB_PROJECT` 影响;默认产物 `caskb-v5-backup-<时间戳>.ckb`)。PG 后端另可 `scripts/backup.sh`(pg_dump)。备份文件异机保存。
- **`gc --keep-last` 精简前先 backup**:`kb gc --keep-last K` 把检索索引裁剪到最近 K 个快照,**已精简的历史索引不可恢复**(数据本体不受影响)。标准顺序:`kb backup` → `kb gc --keep-last K` → `kb fsck`。`KB_GC_PROTECT=on`(默认)会在 gc 前自动导出分支表兜底。
- **restore 前停 serve**:`kb restore <文件> --force` 会先清空目标库,而 serve 进程持有已打开的库连接,热恢复可能读到中间状态或撞锁。维护窗口流程:

```bash
systemctl stop caskb-serve            # 1. 停 serve(SIGTERM 优雅退出)
kb backup                             # 2. 再留一份当前态
kb restore <备份文件> --force          # 3. 恢复(目标非空需 --force)
kb fsck                               # 4. 复核
systemctl start caskb-serve           # 5. 重新对外
```

- **升级版本**:`kb update --yes`(或手动换二进制)**只替换 CLI 本体,不触碰库 schema**。无 schema 变更时(对照 [docs/upgrade.md](upgrade.md) 的版本说明)按滚动方式逐实例操作:停旧(SIGTERM,5s 排空在途写)→ 换二进制 → 起新,单实例窗口秒级;多实例逐台滚动,滚动期间新旧版本共存无害(同库同 schema)。**有 schema 变更时**:走 upgrade.md 的「backup → init 全新库 → restore → `kb index rebuild` → 重新 backup」路径,全程 serve 保持停止,恢复完成并 `fsck` 通过后再对外。

## 5. systemd 与 launchd 示例

两种形态的令牌都经**环境文件引用**,不硬编码进 unit/plist 本体;环境文件属主收敛、权限 600、不入版本库。

### 5.1 systemd(Linux)

```ini
# /etc/systemd/system/caskb-serve.service
[Unit]
Description=cas-kb HTTP API (kb serve)
After=network.target

[Service]
User=caskb
Group=caskb
# 令牌等环境变量集中在此文件(root 可读、权限 600):
#   KB_SERVE_TOKEN=<openssl rand -hex 32 的值>
#   KB_DSN=/var/lib/caskb/caskb.db        # 可选,默认 ~/.local/share/caskb/caskb.db
EnvironmentFile=/etc/caskb/serve.env
ExecStart=/usr/local/bin/kb serve --addr 127.0.0.1:8787
Restart=on-failure
# SIGTERM 触发 serve 优雅退出(停收新请求、5s 排空),停止时限放宽到 15s
TimeoutStopSec=15

[Install]
WantedBy=multi-user.target
```

```bash
# caskb 用户/组不存在则创建,已存在则跳过(groupadd -f 幂等)
sudo groupadd -f caskb && sudo useradd -r -g caskb -s /usr/sbin/nologin caskb
sudo install -d -o caskb -g caskb /var/lib/caskb /etc/caskb
sudo touch /etc/caskb/serve.env && sudo chmod 600 /etc/caskb/serve.env
sudo systemctl daemon-reload && sudo systemctl enable --now caskb-serve
```

### 5.2 launchd(macOS)

launchd 原生没有 EnvironmentFile,用 `sh -c` 先 source 环境文件再 exec,令牌同样只活在文件里:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.caskb.serve</string>
  <!-- 令牌在 ~/.config/caskb/serve.env(权限 600):KB_SERVE_TOKEN=… -->
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string><string>-c</string>
    <string>. "$HOME/.config/caskb/serve.env" &amp;&amp; exec /usr/local/bin/kb serve --addr 127.0.0.1:8787</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/caskb-serve.out.log</string>
  <key>StandardErrorPath</key><string>/tmp/caskb-serve.err.log</string>
</dict>
</plist>
```

```bash
launchctl bootstrap gui/$UID ~/Library/LaunchAgents/com.caskb.serve.plist   # 加载并启动
launchctl kickstart -k gui/$UID/com.caskb.serve                             # 重启(轮换令牌后)
```

两处示例都刻意保持 serve 只绑 `127.0.0.1`;要内网服务时把 `--addr` 换成内网地址并补齐 §1 的加固项。

## 6. 常见问题

### 6.1 端口占用

启动报错 `serve: 监听 127.0.0.1:8787 失败`。排查与处理:

```bash
ss -ltnp 'sport = :8787'            # Linux:谁在监听
lsof -nP -iTCP:8787 -sTCP:LISTEN    # macOS 同效
pgrep -af 'kb serve'                # Linux(procps):列出 PID 与命令行,看有无旧实例
ps -Ao command | grep '[k]b serve'  # macOS:pgrep -af 只输出 PID,须用此等价命令(方括号避免 grep 自匹配)
```

处理:停掉旧实例/占用者,或换端口 `--addr 127.0.0.1:9000`;临时/测试实例可用 `--addr 127.0.0.1:0` 由内核分配端口(实际地址以启动横幅为准)。

### 6.2 两后端锁行为差异(SQLite 文件锁 / PG 行锁)

| | SQLite(默认) | PostgreSQL(`KB_DSN=postgres://…`) |
|---|---|---|
| 并发写模型 | **库级单写者** + busy_timeout:任一写进行中,其他写先等待、超时即锁忙 | 事务 + **行锁**(pgx/v5):冲突面小,写不同项目/不同分支通常互不阻塞 |
| 典型冲突点 | 几乎所有并发写 | 同一分支指针行的推进(同项目同分支的并发写) |
| 锁忙表现 | `SQLITE_BUSY` → 503 | PG lock 类错误 → 503 |

两者由 `store.IsLockBusy` 统一识别,对外都是 §3 的 503 + 可行动提示,调用方无需区分后端。锁忙不丢数据:失败只留未达对象交 GC,不产生半写状态。

### 6.3 日志里不应出现令牌(核验方法)

设计上令牌**只在内存比较,不写日志、不回显**;serve 默认不记请求日志,启动横幅与 401/403 响应都不含令牌值。巡检核验(用 `grep -f` 让令牌文件直接充当模式文件,令牌不出现在命令行、不落临时盘):

```bash
# 期望输出 0;-f 直接读令牌文件做模式,避免令牌进 grep 的 argv
# 判定以输出的计数值 0 为准,勿以 grep 退出码判定(无匹配时 grep 退出码为 1)
grep -cFf <(awk -F= '/^KB_SERVE_TOKEN=/{print $2}' /etc/caskb/serve.env) \
          <(journalctl -u caskb-serve --no-pager)                 # systemd
# launchd 换日志文件:grep -cFf <(awk …) /tmp/caskb-serve.err.log
```

再核两点:1) `ps` 命令列看不到令牌——注意 macOS(BSD pgrep)的 `pgrep -af 'kb serve'` 只输出 PID 不带命令行,须用等价命令 `ps -Ao command | grep '[k]b serve'`(方括号避免 grep 自匹配)才能真正看到命令列;若误用 `--token` 旗标,令牌会出现在命令行上(`ps` 可见、可能进 shell 历史),这正是 §2.1 推荐环境变量注入的原因;2) 环境文件权限确为 600 且未被纳入任何版本库。任一核验命中,按 §2.2 立即轮换并排查泄漏源。

## 7. 安全清单

上线前与周期巡检逐项勾验:

- [ ] **绑定面**:无跨机需求时 serve 只绑 `127.0.0.1`;`ss -ltnp`/`lsof` 巡检无 `0.0.0.0`/`::` 监听,且与部署台账一致
- [ ] **公网隔离**:无任何公网直达路径;跨机消费仅经 SSH 隧道或带 TLS+鉴权的反向代理
- [ ] **令牌强度**:`openssl rand -hex 32`(≥32 字节随机),非口令、非复用值
- [ ] **令牌存放**:只存在于权限 600 的环境文件(`EnvironmentFile` / `serve.env`),不进 unit/plist、不进命令行(`--token` 不用)、不进 shell 历史、不进版本库
- [ ] **令牌分发范围**:名单化管理(谁持有、跑在哪个进程),每部署单元一把,离场/泄露即轮换
- [ ] **只读优先**:无写入需求的 serve 实例一律不配令牌(写端点 403,单写者纪律自动成立)
- [ ] **备份频率**:`kb backup` 按固定周期执行且异机保存;每次升级、`gc --keep-last`、`restore` 前强制先备份
- [ ] **fsck 巡检周期**:固定周期(建议每日或每周)`kb fsck` 零问题;写 API 高峰或异常 503 后追加一次
