package organization

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// dryRunTransaction marks a statement as running inside a transaction without
// a server: DryRun only renders SQL, so the pool is never called.
type dryRunTransaction struct{ gorm.ConnPool }

func (dryRunTransaction) Commit() error   { return nil }
func (dryRunTransaction) Rollback() error { return nil }

func renderGroupPolicyRead(db *gorm.DB) string {
	var rows []groupRow
	return groupPolicyRead(db).Table("iam_groups").Where("tenant_id = ?", 1).Find(&rows).Statement.SQL.String()
}

// TestGroupPolicyReadLocksOnlyInsideMySQLTransactions pins the MySQL
// isolation guard: a grant decision read inside a transaction must lock the
// rows it depends on, because a plain MySQL read is served from an older
// snapshot and would miss a concurrent binding.
func TestGroupPolicyReadLocksOnlyInsideMySQLTransactions(t *testing.T) {
	t.Parallel()
	openMySQL := func() *gorm.DB {
		db, err := gorm.Open(mysql.New(mysql.Config{DSN: "user:pass@tcp(127.0.0.1:1)/organization", SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		return db
	}
	transaction := openMySQL()
	transaction.Statement.ConnPool = dryRunTransaction{ConnPool: transaction.Statement.ConnPool}
	assert.Contains(t, renderGroupPolicyRead(transaction), "FOR UPDATE", "a transactional MySQL read must lock")
	assert.NotContains(t, renderGroupPolicyRead(openMySQL()), "FOR UPDATE", "an autocommit read has nothing to hold a lock for")

	h := newOrganizationHarness(t, "sl_")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		assert.NotContains(t, renderGroupPolicyRead(tx.Session(&gorm.Session{DryRun: true})), "FOR UPDATE", "SQLite has no row locks and rejects the clause")
		return nil
	}))
}
