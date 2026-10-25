package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExecutionCommentSequencesMigrationAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "cseq_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 13))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Omit("ProgressJSON").Create(&parent).Error)
			thread := model.ArtifactCommentThread{ID: 10, TenantID: 7, ProjectID: 3, ExecutionID: 1, WorkItemID: 4, EvidenceID: 9, Visibility: "INTERNAL", State: "RESOLVED", AnchorSnapshot: "{}", FirstBody: "First comment", CreatedBy: 8, CreatedAt: at, Version: 3}
			threads := source.Prefix + thread.TableName()
			messages := source.Prefix + (model.ArtifactCommentMessage{}).TableName()
			conversions := source.Prefix + (model.ArtifactCommentConversion{}).TableName()
			require.NoError(t, db.Table(threads).Omit("AnchorCheckedVersion", "RelocatedAnchor", "AnchorStatus").Create(&thread).Error)
			require.NoError(t, db.Table(messages).Create(map[string]any{"id": 20, "tenant_id": 7, "thread_id": 10, "body": "First comment", "created_by": 8, "created_at": at}).Error)
			for i, kind := range []string{"FEEDBACK", "DEFECT"} {
				require.NoError(t, db.Table(conversions).Create(map[string]any{"id": 30 + i, "tenant_id": 7, "thread_id": 10, "target_kind": kind, "target_id": 40 + i, "actor_id": 8, "created_at": at.Add(time.Duration(i) * time.Second)}).Error)
			}
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 14))
				assertModelMatchesNamedTable(t, raw, engine.dialect, commentMessageSequenceSchema{}, messages)
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.ArtifactCommentConversion{}, conversions)
				var first model.ArtifactCommentMessage
				require.NoError(t, db.Table(messages).Where("id=20").Take(&first).Error)
				require.EqualValues(t, 1, first.Seq)
				require.Equal(t, "PRINCIPAL", first.ActorKind)
				require.True(t, at.Equal(first.CreatedAt))
				var prior []model.ArtifactCommentConversion
				require.NoError(t, db.Table(conversions).Order("id").Find(&prior).Error)
				require.Len(t, prior, 2)
				require.EqualValues(t, 2, prior[0].Seq)
				require.EqualValues(t, 3, prior[1].Seq)
				require.Equal(t, "PRINCIPAL", prior[0].ActorKind)
				require.Equal(t, "PRINCIPAL", prior[1].ActorKind)
				var stored model.ArtifactCommentThread
				require.NoError(t, db.Table(threads).Where("id=10").Take(&stored).Error)
				require.Equal(t, 3, stored.Version, "thread stream matches the existing first message plus canonical conversion history")
				require.Error(t, db.Table(messages).Where("id=20").Update("seq", 0).Error)
				require.Error(t, db.Table(conversions).Where("id=30").Update("seq", 0).Error)
				if cycle == 0 {
					// Migration 18 repairs MySQL's implicit FK support index;
					// its Down retains that index for the published 14 rollback.
					require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 18))
					require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 14))
					require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 13))
				}
			}
			reply := model.ArtifactCommentMessage{ID: 21, TenantID: 7, ThreadID: 10, Seq: 4, Body: "New Human reply", CreatedBy: 8, CreatedAt: at.Add(time.Minute)}
			require.NoError(t, db.Table(messages).Omit("EditCount", "EditedAt", "RetractedAt").Create(&reply).Error)
			duplicate := reply
			duplicate.ID = 22
			require.Error(t, db.Table(messages).Omit("EditCount", "EditedAt", "RetractedAt").Create(&duplicate).Error, "thread sequence cannot repeat")
			require.NoError(t, db.Table(threads).Where("id=10").Updates(map[string]any{"state": "OPEN", "version": 4}).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 13), "legacy schema cannot preserve reply sequencing and refuses downgrade")
			var retained model.ArtifactCommentMessage
			require.NoError(t, db.Table(messages).Where("id=21").Take(&retained).Error)
			require.EqualValues(t, 4, retained.Seq)
			require.True(t, reply.CreatedAt.Equal(retained.CreatedAt))
		})
	}
}

// Frozen schema shape at migration 14; lifecycle metadata lands at migration 17.
type commentMessageSequenceSchema struct {
	ActorKind string    `gorm:"column:actor_kind"`
	Seq       int64     `gorm:"column:seq"`
	ID        int64     `gorm:"column:id"`
	TenantID  int64     `gorm:"column:tenant_id"`
	ThreadID  int64     `gorm:"column:thread_id"`
	Body      string    `gorm:"column:body"`
	CreatedBy int64     `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (commentMessageSequenceSchema) TableName() string { return "execution_artifact_comment_messages" }

func TestExecutionCommentLifecycleMigrationAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "clife_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 16))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "CREATED", ResultState: "NONE", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Create(&parent).Error)
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 17))
				for _, row := range []tableModel{model.ArtifactCommentThread{}, model.ArtifactCommentMessage{}, model.ArtifactCommentEvent{}} {
					assertModelMatchesNamedTable(t, raw, engine.dialect, row, source.Prefix+row.TableName())
				}
				require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 16))
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 17))
			thread := model.ArtifactCommentThread{ID: 10, TenantID: 7, ProjectID: 3, ExecutionID: 1, WorkItemID: 4, EvidenceID: 9, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: "{}", FirstBody: "original", CreatedBy: 8, CreatedAt: at, Version: 2}
			require.NoError(t, db.Table(source.Prefix+thread.TableName()).Create(&thread).Error)
			event := model.ArtifactCommentEvent{ID: 11, TenantID: 7, ThreadID: 10, Seq: 2, Kind: "comment_edited", ActorID: 8, ActorKind: "HUMAN", CreatedAt: at}
			table := source.Prefix + event.TableName()
			require.NoError(t, db.Table(table).Create(&event).Error)
			duplicate := event
			duplicate.ID = 12
			require.Error(t, db.Table(table).Create(&duplicate).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 16))
			require.Equal(t, 1, tableRowCount(t, raw, table))
		})
	}
}
