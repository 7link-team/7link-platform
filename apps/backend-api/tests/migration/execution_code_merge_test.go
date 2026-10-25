package migration_test

import (
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestExecutionCodeMergePopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					raw := engine.open(t)
					source := (&execution.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 14))
					db := openMilestoneGORM(t, raw, engine.dialect)
					at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
					parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 9, WorkItemID: 3, AssignmentID: 6, MachineID: 4, InitiatorID: 11, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "immutable-plan", PlanHash: strings.Repeat("b", 64), DispatchEpoch: 1, LastSeq: 3, Version: 4, CreatedAt: at, UpdatedAt: at}
					parentTable := prefix + parent.TableName()
					require.NoError(t, db.Table(parentTable).Omit("ProgressJSON").Create(&parent).Error)
					table := prefix + (model.Merge{}).TableName()
					for cycle := 0; cycle < 2; cycle++ {
						require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 15))
						assertModelMatchesNamedTable(t, raw, engine.dialect, model.Merge{}, table)
						require.Zero(t, tableRowCount(t, raw, table), "historical execution must not imply a merge")
						active := guid.ID(1)
						pending := model.Merge{ID: 10, TenantID: 7, ProjectID: 9, ExecutionID: 1, MachineID: 4, ActorID: 11, Attempt: 1, ActiveExecutionID: &active, State: "PENDING", AuthoritySource: "ROLE:test", ActorKind: "HUMAN", Reason: "Integrate accepted commit", PlanJSON: "frozen-plan", PlanHash: strings.Repeat("a", 64), CreatedAt: at, DeadlineAt: at.Add(time.Minute)}
						require.NoError(t, db.Table(table).Create(&pending).Error)
						duplicate := pending
						duplicate.ID = 11
						duplicate.Attempt = 2
						require.Error(t, db.Table(table).Create(&duplicate).Error, "only one active merge for an execution")
						duplicate = pending
						duplicate.ID = 12
						duplicate.State = "FAILED"
						duplicate.ActiveExecutionID = nil
						duplicate.FinishedAt = &at
						require.Error(t, db.Table(table).Create(&duplicate).Error, "attempt ordinal is immutable and unique")
						orphan := pending
						orphan.ID = 13
						orphan.TenantID = 8
						require.Error(t, db.Table(table).Create(&orphan).Error, "execution foreign key is tenant scoped")
						invalid := pending
						invalid.ID = 14
						invalid.Attempt = 2
						invalid.ActiveExecutionID = nil
						require.Error(t, db.Table(table).Create(&invalid).Error, "pending merge requires active execution")
						invalid.State = "FAILED"
						require.Error(t, db.Table(table).Create(&invalid).Error, "terminal merge requires a finished timestamp")
						invalid.FinishedAt = &at
						invalid.ActiveExecutionID = &active
						require.Error(t, db.Table(table).Create(&invalid).Error, "failed merge cannot retain active execution")
						require.NoError(t, db.Table(table).Where("id=?", 10).Updates(map[string]any{"state": "FAILED", "active_execution_id": nil, "finished_at": at, "observation_json": "failure", "receipt_hash": strings.Repeat("c", 64)}).Error)
						merged := pending
						merged.ID = 15
						merged.Attempt = 2
						merged.State = "MERGED"
						merged.FinishedAt = &at
						merged.ObservationJSON = "actual merge"
						merged.ReceiptHash = strings.Repeat("d", 64)
						require.NoError(t, db.Table(table).Create(&merged).Error)
						var stored model.Merge
						require.NoError(t, db.Table(table).Where("id=?", 15).Take(&stored).Error)
						require.Equal(t, merged.PlanJSON, stored.PlanJSON)
						require.Equal(t, merged.ReceiptHash, stored.ReceiptHash)
						require.True(t, at.Equal(stored.CreatedAt))
						require.True(t, at.Equal(*stored.FinishedAt))
						require.Error(t, db.Table(parentTable).Where("id=?", 1).Delete(&model.Execution{}).Error, "merge provenance retains its execution")
						require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 14), "persisted merge command and outcome forbid destructive downgrade")
						require.Equal(t, 2, tableRowCount(t, raw, table))
						require.NoError(t, db.Table(table).Where("tenant_id=?", 7).Delete(&model.Merge{}).Error)
						require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 14))
						var existing model.Execution
						require.NoError(t, db.Table(parentTable).Where("id=?", 1).Take(&existing).Error)
						require.Equal(t, parent.PlanJSON, existing.PlanJSON)
						require.Equal(t, parent.PlanHash, existing.PlanHash)
						require.Equal(t, parent.Version, existing.Version)
					}
				})
			}
		})
	}
}
