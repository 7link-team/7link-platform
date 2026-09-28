package gormx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// DatabaseClockMillis reads the database server's Unix millisecond clock.
//
// Lease and replay windows must use the shared database clock, so a node whose
// own clock runs fast cannot reclaim another node's lease early. Every caller
// goes through here, which is what keeps the dialect branch in NowMillisExpr
// the only one in the repository.
func DatabaseClockMillis(ctx context.Context, db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("gormx: database clock requires a database handle")
	}
	var millis int64
	if err := db.WithContext(ctx).Raw("SELECT " + NowMillisExpr(db.Dialector.Name())).Scan(&millis).Error; err != nil {
		return 0, err
	}
	return millis, nil
}

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
