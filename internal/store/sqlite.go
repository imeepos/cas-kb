package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	caskb "github.com/imeepos/cas-kb"

	// 纯 Go SQLite 驱动:无 CGO,默认后端零外部依赖
	_ "modernc.org/sqlite"
)

// SQLite 是 sqlite 后端实现,持有 *sql.DB 连接池。
// 并发模型:WAL + busy_timeout,允许「List 游标遍历中嵌套 Get/Delete」
// (GC/FSCK 依赖该形态);写库由 SQLite 单写者串行化。
type SQLite struct {
	db   *sql.DB
	path string // 人类可读的库文件路径(":memory:" 表示内存库)
}

// openSQLite 打开(含迁移)path 指向的 SQLite 库文件。
// path 支持相对/绝对路径与 ":memory:"(内存库,测试用);
// 容忍 "sqlite:"/"file:" 前缀(与 Open 分派口径一致,避免前缀漏剥生成垃圾文件)。
func openSQLite(ctx context.Context, path string) (*SQLite, error) {
	path = sqlitePath(path)
	if path == "" {
		return nil, fmt.Errorf("store: sqlite 库路径为空")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: 创建库目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("store: 打开 sqlite 失败: %w", err)
	}
	// 连接即校验:文件损坏/非库文件要响亮失败
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: sqlite ping 失败(%s): %w", path, err)
	}
	if err := migrateSQLite(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLite{db: db, path: path}, nil
}

// sqliteDSN 组装 modernc.org/sqlite 的连接串:统一注入会话级 pragma。
// foreign_keys 与 busy_timeout 是每连接生效的,必须走 DSN 而非事后 Exec;
// 内存库走 shared cache,否则连接池各连接拿到的是各自独立的空库。
func sqliteDSN(path string) string {
	var dsn string
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared" // 共享缓存:连接池各连接共见同一内存库
	} else {
		dsn = "file:" + path
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	// 驱动约定:参数名保持字面(_pragma=),仅值中的 '=' 编码为 %3d;
	// 实测参数名若也编码为 %3d,整个参数会被忽略,pragma 全部失效
	return dsn + sep +
		"_pragma=journal_mode%3dWAL" +
		"&_pragma=synchronous%3dNORMAL" +
		"&_pragma=busy_timeout%3d10000" +
		"&_pragma=foreign_keys%3d1"
}

// migrateSQLite 校验已有库版本(不符即在 DDL 前响亮拒绝),再对全新库执行 schema_sqlite.sql。
// 与 PostgreSQL 侧 Migrate 语义一致:meta 表在但无版本行(清空后)等价全新库放行。
func migrateSQLite(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'").Scan(&n); err != nil {
		return fmt.Errorf("store: 检查 meta 表失败: %w", err)
	}
	if n > 0 {
		var v string
		err := db.QueryRowContext(ctx,
			"SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
		switch {
		case err == nil:
			if v != fmt.Sprint(DBSchemaVersion) {
				return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开;"+
					"老数据可弃时删除库文件后重新执行 kb init", v, DBSchemaVersion)
			}
			// 版本相符:DDL 幂等,继续执行以补齐缺表/缺索引
		default:
			if err != sql.ErrNoRows {
				return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
			}
			// meta 表在但无版本行:等价全新库,交由 schema 初始化
		}
	}
	if _, err := db.ExecContext(ctx, caskb.SchemaSQLiteSQL); err != nil {
		return fmt.Errorf("store: 迁移 DDL 失败: %w", err)
	}
	var v string
	if err := db.QueryRowContext(ctx,
		"SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v); err != nil {
		return fmt.Errorf("store: 读取 schema_version 失败: %w", err)
	}
	if v != fmt.Sprint(DBSchemaVersion) {
		return fmt.Errorf("store: 库 schema_version=%s 与实现 %d 不匹配,拒绝打开", v, DBSchemaVersion)
	}
	return nil
}

// Path 返回库文件路径(展示用)。
func (s *SQLite) Path() string { return s.path }

// Close 释放连接池。
func (s *SQLite) Close() error { return s.db.Close() }

// Wipe 清空全部业务数据(事务内 DELETE 四表,顺序满足外键约束),
// 再重跑 schema_sqlite.sql 播种默认项目与 schema_version,结果等价全新初始化的库。
func (s *SQLite) Wipe(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: Wipe 开启事务失败: %w", err)
	}
	defer tx.Rollback() // 提交后为空操作
	for _, stmt := range []string{
		"DELETE FROM branches",
		"DELETE FROM objects",
		"DELETE FROM projects",
		"DELETE FROM meta",
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: Wipe 清空失败(%s): %w", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: Wipe 提交失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, caskb.SchemaSQLiteSQL); err != nil {
		return fmt.Errorf("store: Wipe 后重新播种失败: %w", err)
	}
	return nil
}
