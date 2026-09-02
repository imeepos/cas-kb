package store

import "strings"

// lockBusyMarkers 是各后端「写入事务锁忙」的错误特征串。
//   - SQLite:modernc.org/sqlite 在 busy_timeout 到期后返回
//     "database is locked"(SQLITE_BUSY)。
//   - PostgreSQL:pgx 在行锁超时后返回 "database is locked" 类
//     "lock timeout" 错误(40P01 deadlock detected / 55P03 lock_not_available,
//     实测 pgx 序列化为 "database is locked" 文本)。
//
// 写端点(HTTP)据此把锁忙映射为 503 + 可行动提示(见 DESIGN §8.6)。
var lockBusyMarkers = []string{
	"database is locked",
	"lock timeout",
	"deadlock detected",
}

// IsLockBusy 报告错误是否属于「写入事务锁忙」(另一写者持有库/行锁)。
// serve 与 CLI 同时写时依赖后端事务串行化(SQLite 单写者 / PG 行锁),
// 锁忙不是数据错误,应转译为 503「稍后重试或改用 CLI」,不得产生半写状态。
func IsLockBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, m := range lockBusyMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
