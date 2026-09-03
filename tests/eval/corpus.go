// Package eval 固定语义检索评测集(M6-B 验收,DESIGN §7.3):
// ≥20 条中文知识条目 + ≥10 条语义查询的固定语料,覆盖四类难点——
// 同义不同词(发布/上线 vs 部署/deploy、备忘录 vs 笔记/知识库、登录态 vs 认证/token)、
// 上下位概念(数据库 → PostgreSQL/MySQL、搜索 → bm25 倒排/语义向量、观测 → metrics 告警)、
// 中英混写(REST/api/json、docker image、graphql 端点)、
// 纯代码/ID 类(错误码 ERR-4021、k8s.io/client-go、sha256)。
//
// 语料被 internal/repo 的混合检索评测单测消费(假 Embedder 按「主题词→轴」
// 固定表构造向量,语义近旁 = 共享主题轴;见 hybrid_eval_test.go)。
// 修改语料即改变评测口径,须同步核对相关 recall 断言。
package eval

// Entry 是评测语料的一条知识条目(经 repo.SetNote 写入临时库)。
type Entry struct {
	Path  string
	Title string
	Body  string
}

// Query 是一条评测查询:Text 为原始查询串;Relevant 为人工标注的相关
// 路径全集(recall@5 的分母);Kind ∈ semantic(语义类:查询词与相关条目
// 用词刻意不同/上下位)与 lexical(代码/ID 类:查询词与条目精确共享 token)。
type Query struct {
	Text     string
	Relevant []string
	Kind     string
}

// Entries 是固定语料(23 条,≥20);条目按主题分组,写入顺序即路径字典序。
var Entries = []Entry{
	// —— 部署/发布(axis 0)——
	{"ops/deploy-guide", "deploy 手册", "部署 docker 镜像与 k8s 滚动 rollout:build 后 push 到 registry,再 apply 分批放量"},
	{"ops/rollout-policy", "rollout 策略", "灰度与金丝雀分批引流,观察一段时间再放量"},
	// —— 容器/镜像(axis 1)——
	{"ops/image-scan", "oci 镜像扫描", "镜像漏洞扫描 trivy:容器运行时白名单,基线检查"},
	// —— 性能(axis 6)——
	{"ops/latency-tuning", "延迟治理", "p99 延迟分析与性能治理:先看火焰图再压测"},
	// —— 数据库(axis 2)/备份(axis 10)——
	{"db/postgres-tuning", "PostgreSQL 调优", "postgres 执行计划与 sql 改写:先 explain 再加 btree"},
	{"db/mysql-backup", "MySQL 备份", "mysql 全量备份与增量 backup:xtrabackup 流水线"},
	{"ops/backup-restore", "备份与容灾", "backup 三副本异地容灾,恢复演练每季度一次"},
	// —— 缓存(axis 3)——
	{"cache/redis-hotkey", "redis 热点 key", "redis 缓存命中率与预热:热点 key 拆分与本地缓存"},
	{"cache/memcached-eviction", "memcached 淘汰", "memcached 缓存淘汰策略 lru:大 value 拆包"},
	// —— 接口(axis 4)/安全(axis 5)——
	{"api/rest-contract", "REST 契约", "api 版本化与 json 错误结构:http 状态码含义要稳定"},
	{"api/graphql-gateway", "graphql 网关", "api 网关限流与鉴权:graphql 端点按 schema 收敛"},
	{"sec/token-auth", "token 认证", "认证走 jwt:token 短期有效,refresh 轮换,密码哈希用 argon2"},
	{"sec/rbac", "RBAC 权限", "权限模型 rbac:角色绑定与审计留痕,授权最小化"},
	// —— 检索(axis 7)——
	{"search/bm25-index", "bm25 倒排", "倒排检索与 bm25 打分:分词 2-gram,索引按快照冻结"},
	{"search/semantic-vec", "语义向量检索", "向量 embedding 与余弦相似:语义召回补足词法短板"},
	// —— 并发(axis 8)——
	{"go/concurrency", "goroutine 并发", "goroutine 与 channel 通信:锁竞态用 mutex 保护临界区"},
	{"go/context-timeout", "context 超时", "goroutine 泄漏定位:context 取消与超时传播"},
	// —— 监控(axis 9)——
	{"obs/metrics-alert", "metrics 告警", "监控指标基数爆炸:metrics label 收敛,告警分级"},
	// —— 日志(axis 14)——
	{"obs/log-trace", "log 追踪", "日志结构化与 trace 串联:请求耗时看 span"},
	// —— 文档/笔记(axis 11)——
	{"docs/markdown-notes", "markdown 笔记", "知识库条目即文档:markdown 双向导入导出"},
	{"docs/kb-conventions", "知识库约定", "笔记命名与目录约定:文档归档按项目分组"},
	// —— 纯代码/ID 类(词法精确命中为主)——
	{"code/err-4021", "错误码 ERR-4021", "错误码 ERR-4021:连接池耗尽,详见 internal/pool.go 第 88 行"},
	{"code/client-go-import", "k8s.io/client-go", "import k8s.io/client-go/kubernetes 的 pod 列表分页,sha256 校验清单"},
}

// Queries 是固定评测查询(12 条语义 + 3 条代码/ID = 15 条,≥10)。
// 语义类查询与相关条目刻意用词不同(同义/上下位/中英),词法(BM25)
// 几乎无法命中——这是混合检索要证明的增益;代码/ID 类则两模式都应命中,
// 证明语义腿没有伤害词法精度。
var Queries = []Query{
	// 语义类(同义不同词 / 上下位 / 中英混写)
	{"怎么把服务发布上线", []string{"ops/deploy-guide", "ops/rollout-policy"}, "semantic"},
	{"数据库选型对比", []string{"db/postgres-tuning", "db/mysql-backup"}, "semantic"},
	{"cache 穿透与雪崩", []string{"cache/redis-hotkey", "cache/memcached-eviction"}, "semantic"},
	{"接口鉴权怎么做", []string{"api/rest-contract", "api/graphql-gateway", "sec/token-auth", "sec/rbac"}, "semantic"},
	{"搜索效果不好如何改进", []string{"search/bm25-index", "search/semantic-vec"}, "semantic"},
	{"观测平台怎么搭建", []string{"obs/metrics-alert"}, "semantic"},
	{"协程之间怎么共享变量", []string{"go/concurrency", "go/context-timeout"}, "semantic"},
	{"备忘录太多怎么整理", []string{"docs/markdown-notes", "docs/kb-conventions"}, "semantic"},
	{"docker image 打标签", []string{"ops/deploy-guide", "ops/image-scan", "code/client-go-import"}, "semantic"},
	{"登录态怎么保持", []string{"api/graphql-gateway", "sec/token-auth", "sec/rbac"}, "semantic"},
	{"误删数据怎么找回", []string{"db/mysql-backup", "ops/backup-restore"}, "semantic"},
	{"响应太慢怎么优化", []string{"ops/latency-tuning"}, "semantic"},
	// 代码/ID 类(两模式都必须命中)
	{"ERR-4021", []string{"code/err-4021"}, "lexical"},
	{"client-go", []string{"code/client-go-import"}, "lexical"},
	{"sha256", []string{"code/client-go-import"}, "lexical"},
}
