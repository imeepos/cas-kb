---
name: cas-kb
description: 操作 cas-kb 知识库(CLI `kb` 与 HTTP API)——写笔记、语义/词法检索、多机同步合并、备份与健康自检。当用户要求「存一条知识/查一下库里有没有/同步到另一台机器/备份知识库」或任何涉及 kb 命令、/api/v1 端点的任务时使用本技能。
---

# cas-kb 知识库操作技能

内容寻址(CAS)+ Merkle 树知识库;一切对象不可变,可变状态只有分支指针。核心纪律:**响亮失败,绝不静默降级**——报错文案里永远有下一步动作,照做即可。

## 0. 心智模型(30 秒)

- 写入 = 创建不可变对象 + 推进分支指针;历史 = 快照 DAG(可用 `--at` 时间旅行)
- 检索索引与向量**随快照冻结**,所以「同快照检索结果逐字节可复现」
- 多机同步 = 比哈希、只传缺失对象;分叉用三方合并,不丢数据
- 项目隔离:同一物理库可存多个项目(`-p` / `KB_PROJECT`),互不可见

## 1. 初始化与写入

```bash
kb init                                   # 默认 SQLite:~/.local/share/caskb/caskb.db
kb project create <名> --desc "用途"      # 多项目时先建项目;单项目可省略
kb note set go/concurrency/channel --title 通道 --body "…"   # 路径即地址,父目录自动创建
kb note get go/concurrency/channel        # 读回(首行 path:)
kb note get go/concurrency/channel --json # 机器可读(与 HTTP GET 同构)
kb note ls                                # 全库递归;--json 可用
```

- 单条写入即提交,索引同步完成才算成功(2xx/退出 0 = 检索立即可见)
- **≥几十条用批量**,不要循环单条写(单条写有索引放大):

```bash
kb bulk import notes.jsonl    # 每行 {"path","title","tags":["…"],"body"};一次提交+一次索引
```

- 想攒一批再一次性入库:`note set/rm --stage` 进暂存分支累积(单条成本恒定),`kb commit [-m]` 收束

## 2. 检索

```bash
kb search "查询词" -n 5                   # BM25 词法(默认;标题3/标签2/正文1 加权,结果确定性可复现)
kb search "并发安全" --hybrid             # 语义+词法混合(RRF 融合)——需先做语义初始化(见下)
kb search "…" --hybrid --snippet --json   # --json 增可选 mode/snippet 字段,缺省契约不变
```

**语义检索前置(一次性)**:
```bash
export KB_EMBED_PROVIDER=openai           # 或 ollama(缺省)
export KB_EMBED_MODEL=<嵌入模型名>        # 如 Qwen/Qwen3-Embedding-4B;未设置=向量功能关闭
export OPENAI_API_KEY=… OPENAI_BASE_URL=… # openai 提供者需要
kb index rebuild --embed                  # 全量重建向量(实测 2000 条 ≈2.7 分钟,0 失败)
```

失败语义(一律响亮报错,有三种,全带可行动指引):快照无向量→先 `rebuild --embed`;模型与库内不一致→重跑 rebuild;嵌入服务不可达→检查服务。**不会静默退回词法**——你要么得到混合结果,要么得到明确错误。

数据出域提醒:`openai` 提供者会把笔记标题+正文发送到端点主机;敏感内容用 `ollama` 提供者(本机)。

## 3. 检索(HTTP 方式,免 shell)

```bash
curl "http://127.0.0.1:8787/api/v1/search?q=并发安全&mode=hybrid&snippet=1"
curl "http://127.0.0.1:8787/api/v1/note?path=go/concurrency/channel"
```

serve 未配置嵌入时 mode=hybrid 返回 409 + 配置指引(不是静默降级);其余读端点:/healthz、/api/v1/{projects,tree,log,diff,merge-state}。

## 4. 多机同步与合并

```bash
kb pull <对端DSN>                      # 默认:ff 或响亮拒绝(文案给两条出路及代价)
kb pull <对端DSN> --merge              # 分叉→条目级三方合并;零冲突直接落库(双亲快照)
kb pull <对端DSN> --merge --allow-unrelated   # 两库各自 init 的冷启动(空基线合并)
```

**冲突时**(退出非零,输出逐行冲突清单):分支进入 `<branch>-merge` 中间态,该分支一切直接写被冻结(报错会指路),此时:
```bash
kb note set <冲突路径> --stage --title … --body …   # --stage 升格为裁决动作
kb merge --continue                                  # 落双亲合并快照,清理中间态
kb merge --abort                                     # 或放弃,回到合并前
```
规则:冲突全有或全无——不落提交不动指针;裁决走 `--stage`;合并快照含双亲(`kb log` 可见,HTTP /api/v1/log 的 parents 数组同构)。

## 5. 运维与安全

```bash
kb doctor                    # 一站体检:存储/fsck/版本/配置/gc-保护/serve 探活;fail 才非零
kb fsck                      # 全对象哈希+引用巡检
kb backup [文件]             # 整库 .ckb 导出——以下三个动作之前必做:gc --keep-last / restore / 升级
kb restore <文件> --force    # 恢复前先停 serve
kb gc --keep-last 50         # 历史索引精简(已精简索引不可恢复,数据本体不受影响)
```

- serve 写端点(POST/DELETE /api/v1/note)需要令牌:`kb serve --token …` 或 `KB_SERVE_TOKEN`;**未配置令牌=纯只读**(写请求一律 403)
- 令牌与 OPENAI_API_KEY 同纪律:不落日志、不回显、不进命令行历史(用环境文件注入)
- 恢复/升级流程与双提供者注意事项见 docs/upgrade.md 与 docs/serve.md

## 6. 常见报错 → 处置

| 报错关键词 | 含义 | 处置 |
|---|---|---|
| `需要 --force 才能覆盖;或改用 kb pull --merge` | 真分叉(有共同祖先) | 通常要 `--merge`(无损);`--force` 会丢弃本地独有提交 |
| `无共同历史…--allow-unrelated` | 两库各自 init,冷启动 | 按 README 冷启动三步;合并一次后永久解除 |
| `已是最新` | 无需操作 | — |
| `存在未完成合并…--stage 裁决后 kb merge --continue` | 中间态冻结中 | 裁决或 --abort |
| `该快照无向量索引,先 kb index rebuild --embed` | --hybrid 前置缺失 | 重建或去掉 --hybrid |
| `KB_EMBED_MODEL 未设置` / `写入令牌无效` | 配置缺失 | 按报错内四步指引设置 |

## 7. 边界(不做的事)

- HTTP 写端点只有笔记的 POST/DELETE;stage/bulk/合并收束只在 CLI
- 不自动迁移旧库:schema 不符会拒绝打开并指引(备份→新库→restore)
- 检索可复现边界 = 同快照 + 同嵌入模型(换模型必须 rebuild --embed)
