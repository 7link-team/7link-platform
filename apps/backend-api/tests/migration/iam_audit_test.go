package migration_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIAMAuditBusinessFilterAcrossEngines(t *testing.T) {
	engines := []struct {
		name, dialect string
		open          func(*testing.T) (*sql.DB, string)
	}{
		{"mysql", "mysql", testdb.OpenMySQLWithDSN}, {"postgres", "postgres", testdb.OpenPostgresWithDSN},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, struct {
			name, dialect string
			open          func(*testing.T) (*sql.DB, string)
		}{"mysql8", "mysql", func(t *testing.T) (*sql.DB, string) {
			raw, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, raw.Close()) })
			verifyRealMySQLServer(t, raw)
			return raw, dsn
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw, dsn := engine.open(t)
			source := (&iamaudit.Module{}).MigrationSource()
			source.Prefix = "business_filter_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: engine.dialect, DSN: dsn, Readable: true, Writable: true}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, router.Close()) })
			ids, err := guid.New(1)
			require.NoError(t, err)
			service := iamaudit.NewService(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(source.Prefix)), ids)
			var wanted []guid.ID
			for i := range 9 {
				detail := map[string]any{"project_id": "3", "actor_kind": "AGENT", "authority_source": "POLICY:dispatch_auto"}
				in := iamaudit.EventInput{TenantID: 1, EventType: "dispatch_auto.applied", Outcome: "success", Detail: detail}
				switch i {
				case 1:
					detail["project_id"] = 3
				case 2:
					detail["project_id"] = "33"
				case 3:
					detail["project_id"] = nil
				case 4:
					delete(detail, "actor_kind")
				case 5:
					detail["authority_source"] = "POLICY:DISPATCH_AUTO"
				case 6:
					in.EventType = "dispatchXauto.applied"
				case 7:
					in.TenantID = 2
				case 8:
					in.EventType = "DISPATCH_AUTO.applied"
				}
				event, err := service.Append(t.Context(), in)
				require.NoError(t, err)
				if i < 2 {
					wanted = append(wanted, event.ID)
				}
			}
			filter := iamaudit.Filter{TenantID: 1, ProjectID: 3, Module: "dispatch_auto", ActorKind: "AGENT", AuthoritySource: "POLICY:dispatch_auto", Limit: 1}
			page, total, err := service.List(t.Context(), filter)
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, page, 1)
			require.Equal(t, wanted[1], page[0].ID)
			snapshot, err := service.PrepareExport(t.Context(), filter)
			require.NoError(t, err)
			var output bytes.Buffer
			require.NoError(t, service.WriteExport(t.Context(), snapshot, &output))
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			require.Len(t, lines, 4)
			for i := range 2 {
				var entry struct {
					Event iamaudit.Event `json:"event"`
				}
				require.NoError(t, json.Unmarshal([]byte(lines[i+1]), &entry))
				require.Equal(t, wanted[i], entry.Event.ID)
			}
		})
	}
}

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
					assertArchiveJobSchema(t, db, prefix+"iam_audit_archive_jobs")

					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 0))
					assertTableExists(t, db, engine.dialect, events, false)
					assertTableExists(t, db, engine.dialect, heads, false)
					assertTableExists(t, db, engine.dialect, policies, false)
					assertTableExists(t, db, engine.dialect, prefix+"iam_audit_archive_jobs", false)
				})
			}
		})
	}
}

func assertArchiveJobSchema(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	insert := func(id, tenant, after, head, attempts int64, state string) error {
		_, err := db.ExecContext(t.Context(), fmt.Sprintf(
			"INSERT INTO %s (id, tenant_id, after_sequence, head_sequence, snapshot, state, attempts, object, retain_until, created_at, updated_at) VALUES (%d, %d, %d, %d, '{}', '%s', %d, '{}', '2027-09-06 00:00:00', '2026-09-06 00:00:00', '2026-09-06 00:00:00')",
			table, id, tenant, after, head, state, attempts,
		))
		return err
	}
	require.NoError(t, insert(1, 11, 0, 500, 0, "queued"))
	assert.Error(t, insert(2, 11, 0, 501, 0, "queued"), "replicas cannot create competing jobs at the same cursor")
	require.NoError(t, insert(3, 12, 0, 500, 0, "queued"), "tenant cursors are independent")
	require.NoError(t, insert(4, 11, 500, 600, 1, "complete"))
	assert.Error(t, insert(5, 11, 600, 600, 0, "queued"), "an archive segment cannot be empty")
	assert.Error(t, insert(6, 11, -1, 600, 0, "queued"))
	assert.Error(t, insert(7, 11, 600, 700, -1, "queued"))
	assert.Error(t, insert(8, 11, 600, 700, 0, "unknown"))
	var token, lease, next int64
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT lease_token, lease_until, next_attempt FROM "+table+" WHERE id = 1").Scan(&token, &lease, &next))
	assert.Zero(t, token)
	assert.Zero(t, lease)
	assert.Zero(t, next)
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
