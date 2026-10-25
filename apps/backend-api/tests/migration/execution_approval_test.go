package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestExecutionApprovalConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run through make test-mysql8")
		}
		raw, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = "fixture_approval_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Approval{}, source.Prefix+(model.Approval{}).TableName())
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Now().UTC().Truncate(time.Millisecond)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "RUNNING", ResultState: "NONE", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Create(&parent).Error)
			row := model.Approval{ID: 2, TenantID: 7, ExecutionID: 1, DispatchEpoch: 1, SessionID: 9, ConnEpoch: 1, RequestID: "request1", Tool: "Read", InputJSON: "{}", RequestHash: "hash", State: "PENDING", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			duplicate := row
			duplicate.ID = 3
			require.Error(t, db.Table(table).Create(&duplicate).Error)
			require.Error(t, db.Table(table).Where("id=?", 2).Update("state", "ALLOW").Error)
			require.Error(t, db.Table(table).Where("id=?", 2).Update("state", "BYPASS").Error)
			actor := guid.ID(8)
			require.NoError(t, db.Table(table).Where("id=?", 2).Updates(map[string]any{"state": "ALLOW", "actor_id": actor, "decided_at": now}).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 3), "permission history refuses destructive rollback")
			var retained model.Approval
			require.NoError(t, db.Table(table).Where("id=?", 2).First(&retained).Error)
			require.Equal(t, "ALLOW", retained.State)
		})
	}
}

// Project policy is durable operational history; all dialects retain false
// explicitly and refuse rollback once a Human policy record exists.
func TestExecutionFullAutoPolicyAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run through make test-mysql8")
		}
		raw, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = "fixture_auto_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ProjectExecutionPolicy{}, source.Prefix+(model.ProjectExecutionPolicy{}).TableName())
			db := openMilestoneGORM(t, raw, engine.dialect)
			row := model.ProjectExecutionPolicy{TenantID: 7, ProjectID: 9, AllowFullAuto: false, Version: 1, ActorID: 11, Reason: "Human disallows unattended tools", UpdatedAt: time.Now().UTC().Truncate(time.Millisecond)}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			require.Error(t, db.Table(table).Create(&row).Error, "one policy per tenant/project")
			var got model.ProjectExecutionPolicy
			require.NoError(t, db.Table(table).Where("tenant_id=? AND project_id=?", 7, 9).First(&got).Error)
			require.False(t, got.AllowFullAuto)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 4))
			require.NoError(t, db.Table(table).Where("tenant_id=? AND project_id=?", 7, 9).First(&got).Error)
			require.Equal(t, row.Reason, got.Reason)
			require.NoError(t, db.Table(table).Where("tenant_id=? AND project_id=?", 7, 9).Delete(&row).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 4))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}
