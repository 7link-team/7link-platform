package gormx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestIsSQLiteBusyRecognizesWrappedContentionWithoutRetryingPermanentFailures(t *testing.T) {
	for _, err := range []error{sqlite3.Error{Code: sqlite3.ErrBusy}, sqlite3.Error{Code: sqlite3.ErrLocked}, fmt.Errorf("poll: %w", sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})} {
		assert.True(t, IsSQLiteBusy(err))
	}
	for _, err := range []error{nil, errors.New("database is locked"), sqlite3.Error{Code: sqlite3.ErrConstraint}, &mysql.MySQLError{Number: 1205}} {
		assert.False(t, IsSQLiteBusy(err))
	}
}

type sqlStateError struct{ state string }

func (e sqlStateError) Error() string      { return "sql state " + e.state }
func (e sqlStateError) SQLState() string   { return e.state }
func (e sqlStateError) Unwrap() error      { return nil }
func (e sqlStateError) SQLStateName() bool { return true }

func TestIsTransactionConflictRecognisesWhatTheDatabaseRolledBack(t *testing.T) {
	t.Parallel()

	assert.True(t, IsTransactionConflict(sqlStateError{state: "40P01"}), "postgres deadlock")
	assert.True(t, IsTransactionConflict(sqlStateError{state: "40001"}), "postgres serialization failure")
	assert.True(t, IsTransactionConflict(&mysql.MySQLError{Number: 1213}), "mysql deadlock")

	// Wrapping is how these reach a caller: the owner that failed names itself
	// before passing the error on, and the decision to retry is taken further up.
	wrapped := fmt.Errorf("erase oauth principal data: %w", sqlStateError{state: "40P01"})
	assert.True(t, IsTransactionConflict(wrapped))
}

func TestIsTransactionConflictLeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()

	assert.False(t, IsTransactionConflict(nil))
	assert.False(t, IsTransactionConflict(errors.New("principal is subject to retention")))
	assert.False(t, IsTransactionConflict(sqlStateError{state: "23505"}), "a unique violation repeats identically")
	assert.False(t, IsTransactionConflict(sqlStateError{state: "42P01"}), "a missing table is not contention")

	// A lock wait timeout means the other transaction still holds the rows, so
	// an immediate retry queues for the same wait. Reporting it would turn a
	// capacity problem into a silent loop.
	assert.False(t, IsTransactionConflict(&mysql.MySQLError{Number: 1205}), "mysql lock wait timeout")
	assert.False(t, IsTransactionConflict(&mysql.MySQLError{Number: 1062}), "mysql duplicate entry")
}
