package gormx

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

// Serialization failure and deadlock. A database reports these when it has
// already rolled the transaction back to break a cycle, so the work never
// happened and running it again is the documented response rather than a guess:
// PostgreSQL says so for 40001 and 40P01, MySQL for 1213.
const (
	postgresSerializationFailure = "40001"
	postgresDeadlockDetected     = "40P01"
	mysqlDeadlock                = 1213
)

// IsTransactionConflict reports whether a transaction was aborted by the
// database to resolve contention rather than because the work was wrong.
//
// The distinction matters because the two need opposite handling. A business
// refusal is deterministic and repeating it changes nothing. A conflict is the
// database saying two transactions wanted the same rows in incompatible orders
// and it picked one to roll back; which one it picks is not something the
// application decides, so a transaction that is correct in isolation can lose
// purely because of what else was running at the time.
//
// It deliberately does not report lock-wait timeouts. A timeout means the other
// transaction is still holding what this one wants, so retrying immediately
// walks into the same wait; that is a capacity or a long-transaction problem,
// and treating it as retryable would bury it.
func IsTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		switch sqlState.SQLState() {
		case postgresSerializationFailure, postgresDeadlockDetected:
			return true
		}
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == mysqlDeadlock {
		return true
	}
	return false
}
