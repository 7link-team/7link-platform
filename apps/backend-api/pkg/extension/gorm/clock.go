package gormx

import (
	"fmt"
	"strings"
)

// NowMillisExpr returns a SQL expression for the database server's current
// Unix time in milliseconds. Lease expiry must use the shared database clock
// so application nodes with skewed clocks cannot reclaim a lease early.
func NowMillisExpr(dialect string) string {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "sqlite", "sqlite3":
		return "CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)"
	case "mysql":
		return "CAST(ROUND(UNIX_TIMESTAMP(NOW(3)) * 1000) AS SIGNED)"
	case "pg", "pgsql", "postgres", "postgresql":
		return "CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)"
	default:
		panic(fmt.Sprintf("gormx: NowMillisExpr unsupported dialect %q", dialect))
	}
}
