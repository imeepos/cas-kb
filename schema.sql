-- =====================================================================
-- cas-kb PostgreSQL 数据模型规格(schema v1)
-- 目标库:102 主机 Docker PostgreSQL,数据库名 caskb
-- 本文件是迁移的权威来源:实现侧的迁移逻辑必须与本文件一致
-- =====================================================================

-- 对象表:内容寻址存储(CAS),只增不删(仅 GC 清扫不可达行)
CREATE TABLE IF NOT EXISTS objects (
    addr  text    PRIMARY KEY,            -- 'sha256:<64位小写hex>',内容哈希即主键
    kind  text    NOT NULL,               -- blob | note | tree | snapshot
    size  integer NOT NULL,               -- data 的字节数
    data  bytea   NOT NULL,               -- blob 原始字节;其余为规范 JSON
    CONSTRAINT objects_kind_check CHECK (kind IN ('blob','note','tree','snapshot')),
    CONSTRAINT objects_addr_format_check CHECK (addr ~ '^sha256:[0-9a-f]{64}$')
);

-- 分支表:全库唯一的可变状态(名字 → 快照地址)
CREATE TABLE IF NOT EXISTS branches (
    name       text        PRIMARY KEY,  -- 分支名,如 'main'
    addr       text        NOT NULL REFERENCES objects(addr),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 元数据表:模式版本等;加载时版本不符必须拒绝服务
CREATE TABLE IF NOT EXISTS meta (
    key   text PRIMARY KEY,               -- 'schema_version' 等
    value text NOT NULL
);

INSERT INTO meta (key, value) VALUES ('schema_version', '1')
ON CONFLICT (key) DO NOTHING;

-- 辅助索引:GC 报表与按类型扫描(可选,规模小可不建)
CREATE INDEX IF NOT EXISTS objects_kind_idx ON objects (kind);

-- 写路径约定:
--   对象写入 = INSERT ... ON CONFLICT (addr) DO NOTHING(幂等)
--   分支推进 = INSERT ... ON CONFLICT (name) DO UPDATE(唯一可变写路径)
--   约束验证示例(拒绝场景):
--     INSERT INTO objects(addr, kind, size, data)
--     VALUES ('md5:bad', 'blob', 1, '\x00');   -- 违反 addr_format_check,必须报错
