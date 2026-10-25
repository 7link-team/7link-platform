package migration_test

import (
	"testing"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestExecutionClientThreadMigrationAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "clthread_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 11))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "CREATED", ResultState: "NONE", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Omit("ProgressJSON").Create(&parent).Error)
			table := source.Prefix + (model.ClientCommentLink{}).TableName()
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 12))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.ClientCommentLink{}, table)
				link := model.ClientCommentLink{ThreadID: 10, TenantID: 7, ProjectID: 3, DeliveryID: 9, ItemID: "frozen-item", ContactID: 8, FirstBody: "Rejected source", CreatedBy: 8, CreatedAt: at, AnchorSnapshot: `{"kind":"CODE_LINES","excerpt":"original"}`}
				require.NoError(t, db.Table(table).Create(&link).Error)
				for _, invalid := range []map[string]any{
					{"target_kind": "REJECTION_ITEM", "target_id": nil, "converted_at": at, "converted_by": 8},
					{"target_kind": "REJECTION_ITEM", "target_id": 11, "converted_at": at, "converted_by": nil},
					{"target_kind": "REJECTION_ITEM", "target_id": 11, "converted_at": nil, "converted_by": 8},
					{"target_kind": "REJECTION_ITEM", "target_id": 0, "converted_at": at, "converted_by": 8},
					{"target_kind": "REJECTION_ITEM", "target_id": 11, "converted_at": at, "converted_by": 0},
					{"target_kind": "OTHER", "target_id": 11, "converted_at": at, "converted_by": 8},
					{"target_kind": "", "target_id": 11},
				} {
					require.Error(t, db.Table(table).Where("thread_id=?", 10).Updates(invalid).Error, "conversion invariant: %v", invalid)
				}
				target, contact := guid.ID(11), guid.ID(8)
				require.NoError(t, db.Table(table).Where("thread_id=?", 10).Updates(map[string]any{"target_kind": "REJECTION_ITEM", "target_id": target, "converted_at": at, "converted_by": contact}).Error)
				var stored model.ClientCommentLink
				require.NoError(t, db.Table(table).Where("thread_id=?", 10).Take(&stored).Error)
				require.Equal(t, link.AnchorSnapshot, stored.AnchorSnapshot)
				require.Equal(t, link.FirstBody, stored.FirstBody)
				require.True(t, at.Equal(stored.CreatedAt))
				require.True(t, at.Equal(*stored.ConvertedAt))
				require.Equal(t, target, *stored.TargetID)
				require.Equal(t, contact, *stored.ConvertedBy)
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 11), "populated immutable source rollback must fail")
				require.Equal(t, 1, tableRowCount(t, raw, table))
				require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+parent.TableName()))
				require.NoError(t, db.Table(table).Where("thread_id=?", 10).Delete(&model.ClientCommentLink{}).Error)
				require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 11))
				require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+parent.TableName()))
			}
		})
	}
}
