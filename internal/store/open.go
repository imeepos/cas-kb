package store

import (
	"context"
	"net/url"
	"strings"
)

// Open 打开(含迁移)存储后端,按 DSN 形态分派:
//   - "postgres://" 或 "postgresql://" 前缀 → PostgreSQL(pgx/v5)
//   - 其余一律视为 SQLite 本地库文件路径("sqlite:" 前缀可省略;
//     ":memory:" 为内存库;相对路径相对进程工作目录解析)
//
// 默认后端是 SQLite(KB_DSN 未设置时由调用方给默认文件路径),
// PostgreSQL 通过 KB_DSN 指向 postgres:// 继续使用。
func Open(ctx context.Context, dsn string) (Store, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return openPostgres(ctx, dsn)
	}
	return openSQLite(ctx, sqlitePath(dsn))
}

// sqlitePath 从用户 DSN 中提取 SQLite 库文件路径(剥掉可选的 sqlite:/file: 前缀)。
func sqlitePath(dsn string) string {
	p := strings.TrimPrefix(dsn, "sqlite:")
	p = strings.TrimPrefix(p, "file:")
	return p
}

// DescribeBackend 返回 DSN 的后端名与展示目标,供 CLI 打印。
// 展示目标绝不包含凭据:postgres 只回显 host/database,sqlite 回显文件路径。
func DescribeBackend(dsn string) (name, target string) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		name = "postgres"
		if u, err := url.Parse(dsn); err == nil && u.Host != "" {
			target = u.Host + u.Path
		}
		return name, target
	}
	return "sqlite", sqlitePath(dsn)
}

// compile-time 断言:两个实现都满足 Store 契约。
var (
	_ Store = (*PG)(nil)
	_ Store = (*SQLite)(nil)
)
