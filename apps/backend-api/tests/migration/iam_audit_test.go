package migration_test

import (
	"database/sql"
	"fmt"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	auditHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	auditHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestIAMAuditMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					source := (&iamaudit.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))

					events := prefix + "iam_audit_events"
					heads := prefix + "iam_audit_heads"
					policies := prefix + "iam_audit_retention_policies"
					insertAuditEvent(t, db, events, 1, 0, "success", 1, auditHashA)

					var principalID, targetID sql.NullInt64
					var detail string
					require.NoError(t, db.QueryRowContext(t.Context(),
						"SELECT principal_id, target_id, detail FROM "+events+" WHERE id = 1",
					).Scan(&principalID, &targetID, &detail))
					assert.False(t, principalID.Valid, "an absent principal must be stored as NULL")
					assert.False(t, targetID.Valid, "an absent target must be stored as NULL")
					assert.Equal(t, "{}", detail, "an absent detail must use the empty-object default")

					_, err := db.ExecContext(t.Context(), fmt.Sprintf(
						"INSERT INTO %s (id, tenant_id, event_type, outcome, detail, created_at, sequence, event_hash) VALUES (2, 0, 'invalid', 'unknown', '{}', '2026-09-02 00:00:00', 2, '%s')",
						events, auditHashB,
					))
					assert.Error(t, err, "outcome CHECK must reject unknown values")

					_, err = db.ExecContext(t.Context(), fmt.Sprintf(
						"INSERT INTO %s (id, tenant_id, event_type, outcome, detail, created_at, sequence, event_hash) VALUES (3, 0, 'duplicate_sequence', 'success', '{}', '2026-09-02 00:00:00', 1, '%s')",
						events, auditHashB,
					))
					assert.Error(t, err, "a tenant chain sequence must be unique")

					_, err = db.ExecContext(t.Context(), fmt.Sprintf(
						"INSERT INTO %s (id, tenant_id, event_type, outcome, detail, created_at, sequence, event_hash) VALUES (4, 0, 'duplicate_hash', 'success', '{}', '2026-09-02 00:00:00', 2, '%s')",
						events, auditHashA,
					))
					assert.Error(t, err, "a tenant chain event hash must be unique")

					_, err = db.ExecContext(t.Context(), "UPDATE "+events+" SET outcome = 'failure' WHERE id = 1")
					assert.ErrorContains(t, err, "append-only")
					_, err = db.ExecContext(t.Context(), "DELETE FROM "+events+" WHERE id = 1")
					assert.ErrorContains(t, err, "append-only")

					_, err = db.ExecContext(t.Context(), fmt.Sprintf(
						"INSERT INTO %s (tenant_id, sequence, event_hash, updated_at) VALUES (0, 0, '', '2026-09-02 00:00:00')",
						heads,
					))
					require.NoError(t, err, "tenant zero is the platform audit scope")
					insertRetentionPolicy(t, db, policies, 1, 365, 730)
					assertRetentionPolicyRejected(t, db, policies, 2, 0, 1)
					assertRetentionPolicyRejected(t, db, policies, 3, 10, 9)
					assertRetentionPolicyRejected(t, db, policies, 4, 1, 36501)

					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 0))
					assertTableExists(t, db, engine.dialect, events, false)
					assertTableExists(t, db, engine.dialect, heads, false)
					assertTableExists(t, db, engine.dialect, policies, false)
				})
			}
		})
	}
}

func insertAuditEvent(t *testing.T, db *sql.DB, table string, id, tenantID int64, outcome string, sequence int64, hash string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (id, tenant_id, event_type, outcome, created_at, sequence, event_hash) VALUES (%d, %d, 'event', '%s', '2026-09-02 00:00:00', %d, '%s')",
		table, id, tenantID, outcome, sequence, hash,
	))
	require.NoError(t, err)
}

func insertRetentionPolicy(t *testing.T, db *sql.DB, table string, tenantID int64, minDays, archiveDays int) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (tenant_id, min_days, archive_after_days, updated_at) VALUES (%d, %d, %d, '2026-09-02 00:00:00')",
		table, tenantID, minDays, archiveDays,
	))
	require.NoError(t, err)
}

func assertRetentionPolicyRejected(t *testing.T, db *sql.DB, table string, tenantID int64, minDays, archiveDays int) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (tenant_id, min_days, archive_after_days, updated_at) VALUES (%d, %d, %d, '2026-09-02 00:00:00')",
		table, tenantID, minDays, archiveDays,
	))
	assert.Error(t, err)
}
