package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTransactionCoordinatorRollsBackMutationRevisionAndAudit(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	require.NoError(t, harness.db.Exec(
		"CREATE TABLE sl_test_audit (event_type TEXT NOT NULL)",
	).Error)
	expected := errors.New("audit unavailable")
	coordinator := newTransactionCoordinator(harness.repository, func(
		ctx context.Context,
		db *gorm.DB,
		event audit.DomainEvent,
	) error {
		if err := db.WithContext(ctx).Exec(
			"INSERT INTO sl_test_audit (event_type) VALUES (?)", event.EventType,
		).Error; err != nil {
			return err
		}
		return expected
	})
	record := newAuditRecord(t.Context(), 1, "role_created", "role", 0)
	record.PolicyChanged = true
	err := coordinator.Run(t.Context(), record, func(repository *Repository) error {
		_, err := repository.CreateRole(t.Context(), 1, "Rolled Back", "")
		return err
	})
	assert.ErrorIs(t, err, expected)
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_iam_roles"))
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_iam_policy_revisions"))
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_test_audit"))

	coordinator.audit = func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		return db.WithContext(ctx).Exec(
			"INSERT INTO sl_test_audit (event_type) VALUES (?)", event.EventType,
		).Error
	}
	record = newAuditRecord(t.Context(), 1, "role_created", "role", 0)
	record.PolicyChanged = true
	require.NoError(t, coordinator.Run(t.Context(), record, func(repository *Repository) error {
		_, err := repository.CreateRole(t.Context(), 1, "Committed", "")
		return err
	}))
	assert.Equal(t, int64(1), testTableCount(t, harness.db, "sl_iam_roles"))
	assert.Equal(t, int64(1), testTableCount(t, harness.db, "sl_iam_policy_revisions"))
	assert.Equal(t, int64(1), testTableCount(t, harness.db, "sl_test_audit"))
}

func TestTransactionCoordinatorPinsOneWriter(t *testing.T) {
	prefix := "sl_"
	directory := t.TempDir()
	dsns := []string{
		filepath.Join(directory, "writer-a.sqlite"),
		filepath.Join(directory, "writer-b.sqlite"),
	}
	for _, dsn := range dsns {
		initializeRepositoryDatabase(t, dsn, prefix)
		db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
		require.NoError(t, err)
		require.NoError(t, db.Exec("CREATE TABLE sl_test_audit (event_type TEXT NOT NULL)").Error)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	}
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"a": {Type: "sqlite", DSN: dsns[0], Writable: true},
		"b": {Type: "sqlite", DSN: dsns[1], Writable: true},
	})
	require.NoError(t, err)
	runtime := appenv.NewRuntime(
		appenv.NewIdentity("iam-test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	repository := NewRepository(runtime, newRepositoryTestIDs(2000))
	repository.now = func() time.Time { return repositoryTestTime }
	coordinator := newTransactionCoordinator(repository, func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		return db.WithContext(ctx).Exec("INSERT INTO sl_test_audit (event_type) VALUES (?)", event.EventType).Error
	})
	record := newAuditRecord(t.Context(), 1, "role_created", "role", 0)
	record.PolicyChanged = true
	require.NoError(t, coordinator.Run(t.Context(), record, func(repository *Repository) error {
		_, err := repository.CreateRole(t.Context(), 1, "Single Writer", "")
		return err
	}))
	require.NoError(t, router.Close())

	states := make([][3]int64, len(dsns))
	for index, dsn := range dsns {
		db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Readable: true}).Open(t.Context())
		require.NoError(t, err)
		states[index] = [3]int64{
			testTableCount(t, db, "sl_iam_roles"),
			testTableCount(t, db, "sl_iam_policy_revisions"),
			testTableCount(t, db, "sl_test_audit"),
		}
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	}
	assert.ElementsMatch(t, [][3]int64{{1, 1, 1}, {0, 0, 0}}, states)
}

func testTableCount(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table(table).Count(&count).Error)
	return count
}
