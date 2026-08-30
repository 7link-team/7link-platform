package audit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationSource(t *testing.T) {
	source := migrationSource()
	assert.Equal(t, "goose_audit", source.TableName)
	assert.NotNil(t, source.FS)

	// The runtime module satisfies the application migration runner contract.
	auditModule := NewModule("svc", "1", nil, Config{Enabled: true})
	assert.Equal(t, "audit", auditModule.ModuleName())
	require.NotNil(t, auditModule.MigrationSource())
	require.Equal(t, source.TableName, auditModule.MigrationSource().TableName)
}

func TestMigrateCreatesTable(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, Migrate(t.Context(), db))
	require.True(t, db.Migrator().HasTable("operation_logs"))

	// Migrating again is idempotent.
	require.NoError(t, Migrate(t.Context(), db))
}

func TestMigrateUpAndDown(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, MigrateUp(t.Context(), db))
	require.True(t, db.Migrator().HasTable("operation_logs"))
	require.NoError(t, MigrateDown(t.Context(), db))
	require.False(t, db.Migrator().HasTable("operation_logs"))
}

func TestMigrateRequiresWriter(t *testing.T) {
	assert.ErrorIs(t, Migrate(t.Context(), nil), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateUp(t.Context(), nil), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateDown(t.Context(), nil), ErrDatabaseUnavailable)
}

func TestOperationLogTableName(t *testing.T) {
	assert.Equal(t, operationLogsTable, (OperationLog{}).TableName())
	_ = context.Background()
}
