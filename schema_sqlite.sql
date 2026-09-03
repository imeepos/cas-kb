-- =====================================================================
-- cas-kb SQLite 数据模型规格(schema v6)
-- 本文件是 schema.sql(PostgreSQL 权威规格)的语义镜像,供 SQLite 后端使用:
-- 表/列/约束/默认播种/索引一一对应,库 schema 版本一致(当前 6)。
-- 两文件必须同步演进:任何数据模型变更两处一起改,版本号一致。
-- 与 PostgreSQL 的刻意差异(仅类型与方言,不改语义):
--   bytea → BLOB;timestamptz+now() → TEXT+strftime(UTC ISO8601);
--   正则 CHECK(addr ~ ...) → 等价的 GLOB+长度 CHECK;
--   TRUNCATE 语义由 store 层事务内 DELETE 实现(kb wipe)。
-- 不做存量库自动迁移:版本不符时实现侧拒绝打开;老数据可弃则删库重建。
-- =====================================================================

-- 对象表:内容寻址存储(CAS),只增不删(仅 GC 清扫不可达行)
-- 对象全局共享、跨项目去重:归属关系由「项目分支头 → 可达性」决定,
-- 不在对象上冗余标注项目。
CREATE TABLE IF NOT EXISTS objects (
    addr  text    PRIMARY KEY,            -- 'sha256:<64位小写hex>',内容哈希即主键
    kind  text    NOT NULL,               -- blob | note | tree | snapshot | indexroot | indexshard | vecroot | vecshard
    size  integer NOT NULL,               -- data 的字节数
    data  blob    NOT NULL,               -- blob 原始字节;其余为规范 JSON
    CONSTRAINT objects_kind_check CHECK (kind IN ('blob','note','tree','snapshot','indexroot','indexshard','vecroot','vecshard')),
    CONSTRAINT objects_addr_format_check CHECK (
        length(addr) = 71 AND addr GLOB 'sha256:[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'
    )
);

-- 项目表:项目隔离的一等实体;分支按项目划分命名空间。
-- 外键约束保证写入分支时项目必须已存在(误配置响亮失败)。
-- description:项目用途说明(存什么、何时引用),供 AI/人快速选用;
-- 属命名空间层元数据:就地更新、不产生快照;约定 ≤512 字符。
CREATE TABLE IF NOT EXISTS projects (
    name        text PRIMARY KEY,         -- 项目名,如 'go-server'、'frontend'
    created_at  text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    description text NOT NULL DEFAULT ''
);

-- 分支表:可变命名空间((project, name) → 快照地址 + 描述)
-- 项目隔离的根:每个项目的快照 DAG 由本项目的分支头唯一可达,
-- 数据隔离由可达性天然获得;objects 保持全局共享以保留去重红利。
-- description:分支用途说明(如「工作线」「归档线」),供 AI 判断读写目标;
-- 就地更新;分支推进的 UPSERT 不得覆盖既有描述;约定 ≤512 字符。
CREATE TABLE IF NOT EXISTS branches (
    project     text NOT NULL DEFAULT 'default' REFERENCES projects(name),
    name        text NOT NULL,            -- 分支名,如 'main'(项目内唯一)
    addr        text NOT NULL REFERENCES objects(addr),
    updated_at  text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    description text NOT NULL DEFAULT '',
    PRIMARY KEY (project, name)
);

-- 默认项目:v1 存量数据的落点;未指定项目时的行为保持与 v1 一致
INSERT INTO projects (name) VALUES ('default') ON CONFLICT (name) DO NOTHING;

-- 元数据表:库 schema 版本等;加载时版本不符必须拒绝服务
CREATE TABLE IF NOT EXISTS meta (
    key   text PRIMARY KEY,               -- 'schema_version' 等
    value text NOT NULL
);

INSERT INTO meta (key, value) VALUES ('schema_version', '6')
ON CONFLICT (key) DO NOTHING;

-- 辅助索引:与 schema.sql 一致(GC 报表与按类型扫描;项目维度列分支)
CREATE INDEX IF NOT EXISTS objects_kind_idx ON objects (kind);
CREATE INDEX IF NOT EXISTS branches_project_idx ON branches (project);

-- 写路径约定(与 schema.sql 相同):
--   对象写入 = INSERT ... ON CONFLICT (addr) DO NOTHING(幂等,全局去重)
--   分支推进 = INSERT ... ON CONFLICT (project, name) DO UPDATE SET addr/updated_at
--             (UPDATE 集不含 description:推进不清空描述)
--   项目创建 = INSERT INTO projects(可带描述;default 由本文件兜底,描述为空)
--   描述更新 = UPDATE projects/branches.description(命名空间元数据,不产生快照)
--   约束验证示例(拒绝场景):
--     INSERT INTO objects(addr, kind, size, data)
--     VALUES ('md5:bad', 'blob', 1, x'00');   -- 违反 addr 格式 CHECK,必须报错
--     INSERT INTO branches(project, name, addr)
--     VALUES ('ghost', 'main', 'sha256:...'); -- 项目不存在,违反外键,必须报错
