package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExecutionEvidenceContextMigrationAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "evctx_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 12))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			// Only case 1 has exact committed ordinary claim-production proof.
			cases := []struct {
				state, branch string
				receipt       bool
				epoch, seq    int64
			}{
				{"SUCCEEDED", "", true, 2, 3},
				{"SUCCEEDED", "", false, 2, 3},
				{"FAILED", "", true, 2, 3},
				{"SUCCEEDED", "ENSEMBLE", true, 2, 3},
				{"SUCCEEDED", "SKILL_DISTILLATION", true, 2, 3},
				{"SUCCEEDED", "", true, 1, 3},
				{"SUCCEEDED", "", true, 2, 2},
			}
			evidenceTable := source.Prefix + (model.Evidence{}).TableName()
			for i, fixture := range cases {
				id := guid.ID(i + 1)
				parent := model.Execution{ID: id, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: fixture.state, StateReason: "CLAIM_SUBMITTED", ResultState: "CONFIRMED", BranchReason: fixture.branch, PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 2, LastSeq: 3, Version: 1, CreatedAt: at, UpdatedAt: at}
				require.NoError(t, db.Table(source.Prefix+parent.TableName()).Omit("ProgressJSON").Create(&parent).Error)
				if fixture.receipt {
					receipt := model.Receipt{ID: id + 100, TenantID: 7, ExecutionID: id, DispatchEpoch: fixture.epoch, Seq: fixture.seq, Kind: "COMPLETED", State: "SUCCEEDED", ContentHash: "hash", CreatedAt: at}
					require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Create(&receipt).Error)
				}
				// A map deliberately omits fields that do not exist before migration 13.
				require.NoError(t, db.Table(evidenceTable).Create(map[string]any{"id": id + 200, "tenant_id": 7, "project_id": 3, "execution_id": id, "kind": "OBSERVATION", "title": "legacy", "content": "persisted observation", "content_hash": "hash", "created_at": at}).Error)
			}
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 13))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.Evidence{}, evidenceTable)
				var rows []model.Evidence
				require.NoError(t, db.Table(evidenceTable).Order("id").Find(&rows).Error)
				require.Len(t, rows, len(cases))
				for i, row := range rows {
					require.Equal(t, "SYSTEM", row.ProducerKind)
					require.Zero(t, row.ProducerID)
					require.Nil(t, row.ScopeText)
					require.Nil(t, row.Limitations)
					require.Nil(t, row.ObservedAt)
					require.True(t, at.Equal(row.CreatedAt))
					if i == 0 {
						require.Equal(t, "ATTACHED", row.State)
						require.NotNil(t, row.ClaimID)
						require.Equal(t, guid.ID(1), *row.ClaimID)
					} else {
						require.Equal(t, "DRAFT", row.State)
						require.Nil(t, row.ClaimID)
					}
				}
				for _, invalid := range []map[string]any{
					{"state": "UNKNOWN"}, {"state": "ATTACHED"}, {"state": "ATTACHED", "claim_id": 0},
					{"state": "DRAFT", "claim_id": 1}, {"state": "WITHDRAWN", "claim_id": 1},
					{"producer_kind": "UNKNOWN"}, {"producer_kind": "SYSTEM", "producer_id": 1},
					{"producer_kind": "AGENT", "producer_id": 0}, {"producer_kind": "HUMAN", "producer_id": -1},
					{"producer_kind": "MACHINE", "producer_id": 0},
				} {
					require.Error(t, db.Table(evidenceTable).Where("id = ?", 202).Updates(invalid).Error, "invalid evidence invariants: %v", invalid)
				}
				require.NoError(t, db.Table(evidenceTable).Where("id = ?", 202).Updates(map[string]any{"producer_kind": "AGENT", "producer_id": 9, "scope_text": "current scope", "limitations": "observed only", "observed_at": at, "state": "WITHDRAWN"}).Error)
				var stored model.Evidence
				require.NoError(t, db.Table(evidenceTable).Where("id = ?", 202).Take(&stored).Error)
				require.Equal(t, "AGENT", stored.ProducerKind)
				require.Equal(t, guid.ID(9), stored.ProducerID)
				require.Equal(t, "WITHDRAWN", stored.State)
				require.NotNil(t, stored.ObservedAt)
				require.True(t, at.Equal(*stored.ObservedAt), "post-2038 metadata retains instant and milliseconds")
				if cycle == 0 {
					require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 12))
					require.Equal(t, len(cases), tableRowCount(t, raw, evidenceTable), "rollback preserves original evidence facts")
				}
			}
		})
	}
}
