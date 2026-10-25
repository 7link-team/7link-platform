package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestExecutionPreviewModelsAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "preview_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []tableModel{model.PreviewSession{}, model.PreviewAccess{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, source.Prefix+row.TableName())
				assertTableExists(t, raw, engine.dialect, row.TableName(), false)
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			row := model.PreviewSession{ID: 601, TenantID: 7, ProjectID: 301, ExecutionID: 401, MachineID: 501, RunnerSessionID: 701, ConnEpoch: 4294967297, DispatchEpoch: 4294967298, Port: 8080, Label: "abcdefghijklmnopqrstuv2345", State: "READY", StartedBy: 901, CreatedAt: at, LastAccessAt: at.Add(time.Second), ExpiresAt: at.Add(time.Hour), Version: 1}
			sessions := source.Prefix + row.TableName()
			require.NoError(t, db.Table(sessions).Create(&row).Error)
			var stored model.PreviewSession
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Take(&stored).Error)
			require.Equal(t, row.Label, stored.Label)
			require.Equal(t, row.ConnEpoch, stored.ConnEpoch)
			require.Equal(t, row.DispatchEpoch, stored.DispatchEpoch)
			require.Nil(t, stored.EndedAt)
			for _, pair := range [][2]time.Time{{row.CreatedAt, stored.CreatedAt}, {row.LastAccessAt, stored.LastAccessAt}, {row.ExpiresAt, stored.ExpiresAt}} {
				require.True(t, pair[0].Equal(pair[1]), "post-2038 timestamps retain instant and milliseconds")
			}
			require.Error(t, db.Table(sessions).Where("id = ?", row.ID).Update("id", nil).Error, "preview identity cannot become NULL")
			duplicate := row
			duplicate.ID, duplicate.TenantID = 602, 8
			require.Error(t, db.Table(sessions).Create(&duplicate).Error, "opaque labels identify one session globally, even across tenants")
			duplicate.Label = "bcdefghijklmnopqrstuv23456"
			require.NoError(t, db.Table(sessions).Create(&duplicate).Error)

			access := model.PreviewAccess{ID: 801, SessionID: row.ID, PrincipalID: 901, CodeHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CodeExpiresAt: at.Add(time.Minute), ReturnPath: "/assets/main.js?mode=preview"}
			accesses := source.Prefix + access.TableName()
			require.NoError(t, db.Table(accesses).Create(&access).Error)
			var storedAccess model.PreviewAccess
			require.NoError(t, db.Table(accesses).Where("id = ?", access.ID).Take(&storedAccess).Error)
			require.Equal(t, access.CodeHash, storedAccess.CodeHash)
			require.Equal(t, access.ReturnPath, storedAccess.ReturnPath)
			require.True(t, access.CodeExpiresAt.Equal(storedAccess.CodeExpiresAt))
			require.Nil(t, storedAccess.ConsumedAt)
			require.Nil(t, storedAccess.CookieHash)
			require.Nil(t, storedAccess.CookieExpiresAt)
			require.Error(t, db.Table(accesses).Where("id = ?", access.ID).Update("id", nil).Error, "access identity cannot become NULL")
			second := access
			second.ID, second.SessionID = 802, duplicate.ID
			require.Error(t, db.Table(accesses).Create(&second).Error, "one code digest cannot authorize different sessions")
			second.CodeHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			require.NoError(t, db.Table(accesses).Create(&second).Error, "multiple unexchanged grants may have NULL cookie digests")
			cookieHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			cookieExpires := at.Add(30 * time.Minute)
			require.NoError(t, db.Table(accesses).Where("id = ?", access.ID).Updates(map[string]any{"consumed_at": at, "cookie_hash": cookieHash, "cookie_expires_at": cookieExpires}).Error)
			require.Error(t, db.Table(accesses).Where("id = ?", second.ID).Update("cookie_hash", cookieHash).Error, "one cookie digest cannot authorize different sessions")
			storedAccess = model.PreviewAccess{}
			require.NoError(t, db.Table(accesses).Where("id = ?", access.ID).Take(&storedAccess).Error)
			require.NotNil(t, storedAccess.ConsumedAt)
			require.True(t, at.Equal(*storedAccess.ConsumedAt))
			require.NotNil(t, storedAccess.CookieHash)
			require.Equal(t, cookieHash, *storedAccess.CookieHash)
			require.NotNil(t, storedAccess.CookieExpiresAt)
			require.True(t, cookieExpires.Equal(*storedAccess.CookieExpiresAt))
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Updates(map[string]any{"state": "STOPPED", "stop_reason": "USER", "ended_at": at, "version": 2}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect), "preview migration rerun is idempotent")
			stored = model.PreviewSession{}
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Take(&stored).Error)
			require.Equal(t, "STOPPED", stored.State)
			require.Equal(t, "USER", stored.StopReason)
			require.Equal(t, 2, stored.Version)
			require.NotNil(t, stored.EndedAt)
			require.True(t, at.Equal(*stored.EndedAt))
			require.Equal(t, 2, tableRowCount(t, raw, sessions))
			require.Equal(t, 2, tableRowCount(t, raw, accesses))
		})
	}
}

func TestExecutionModelsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []tableModel{model.Execution{}, model.Receipt{}, model.Log{}, model.Evidence{}, model.VerificationTask{}, model.ArtifactCommentThread{}, model.ArtifactCommentMessage{}, model.ArtifactCommentConversion{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, deploymentPrefix+row.TableName())
			}
		})
	}
}

func TestExecutionArtifactCommentConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = "artifact_comment_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Now().UTC().Truncate(time.Millisecond)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Create(&parent).Error)
			thread := model.ArtifactCommentThread{ID: 10, TenantID: 7, ProjectID: 3, ExecutionID: 1, WorkItemID: 4, EvidenceID: 9, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: `{}`, FirstBody: "Frozen first comment", CreatedBy: 8, CreatedAt: now, Version: 1}
			table := source.Prefix + thread.TableName()
			require.NoError(t, db.Table(table).Create(&thread).Error)
			for field, value := range map[string]any{"visibility": "UNKNOWN", "state": "UNKNOWN", "version": 0, "execution_id": 999, "tenant_id": 999} {
				require.Error(t, db.Table(table).Where("id = ?", thread.ID).Update(field, value).Error, field)
			}
			message := model.ArtifactCommentMessage{ID: 11, TenantID: 7, ThreadID: 10, Body: "Frozen first comment", CreatedBy: 8, CreatedAt: now}
			require.NoError(t, db.Table(source.Prefix+message.TableName()).Create(&message).Error)
			message.ID, message.TenantID = 12, 999
			require.Error(t, db.Table(source.Prefix+message.TableName()).Create(&message).Error, "message cannot reference another tenant's thread")
			conversion := model.ArtifactCommentConversion{ID: 20, TenantID: 7, ThreadID: 10, TargetKind: "DEFECT", TargetID: 30, ActorID: 8, CreatedAt: now}
			conversions := source.Prefix + conversion.TableName()
			require.NoError(t, db.Table(conversions).Create(&conversion).Error)
			conversion.ID = 21
			require.Error(t, db.Table(conversions).Create(&conversion).Error, "one defect conversion per thread")
			conversion.TargetKind, conversion.Seq = "FEEDBACK", 3
			require.NoError(t, db.Table(conversions).Create(&conversion).Error, "feedback and defect conversions coexist")
			conversion.ID, conversion.TargetKind = 22, "UNKNOWN"
			require.Error(t, db.Table(conversions).Create(&conversion).Error)
			require.Error(t, db.Table(table).Where("id = ?", thread.ID).Delete(&model.ArtifactCommentThread{}).Error, "referenced source is retained")
		})
	}
}

func TestExecutionQualityLineageAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Now().UTC().Truncate(time.Millisecond)
			parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			table := deploymentPrefix + parent.TableName()
			require.NoError(t, db.Table(table).Create(&parent).Error)
			child := parent
			child.ID = 2
			child.State = "CREATED"
			child.InitiatorID = 0
			child.ParentExecutionID = &parent.ID
			child.SourceDecisionID = new(parent.ID + 76)
			child.BranchReason = "QUALITY"
			child.StartPoint = "CONTINUE"
			child.FeedbackSnapshot = `{"action":"REQUEST_CHANGES","detail":"observed failure"}`
			child.Autostart = true
			require.NoError(t, db.Table(table).Create(&child).Error)
			duplicate := child
			duplicate.ID = 3
			require.Error(t, db.Table(table).Create(&duplicate).Error, "source-decision uniqueness survives independent insertion")
			require.Error(t, db.Table(table).Where("id=?", child.ID).Update("source_decision_id", 0).Error)
			require.Error(t, db.Table(table).Where("id=?", child.ID).Update("source_decision_id", nil).Error, "parent requires a source decision")
			require.Error(t, db.Table(table).Where("id=?", child.ID).Update("parent_execution_id", nil).Error, "source decision requires a parent")
			var stored model.Execution
			require.NoError(t, db.Table(table).Where("id=?", child.ID).First(&stored).Error)
			require.Equal(t, child.SourceDecisionID, stored.SourceDecisionID)
			require.Equal(t, child.ParentExecutionID, stored.ParentExecutionID)
			require.Equal(t, child.FeedbackSnapshot, stored.FeedbackSnapshot)
			require.True(t, stored.Autostart)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 2), "durable source history cannot be dropped")
			stored = model.Execution{}
			require.NoError(t, db.Table(table).Where("id=?", child.ID).First(&stored).Error)
			require.Equal(t, child.FeedbackSnapshot, stored.FeedbackSnapshot)
		})
	}
}

func TestExecutionQualityEmptyRollbackAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 2), "empty lineage can be rolled back without leaving a cross-column check")
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect), "quality lineage can be reapplied after rollback")
		})
	}
}

// The in-memory MySQL protocol engine has no temporary tables; exercise the
// rollback guard and named cross-column CHECK on the actual supported server.
func TestExecutionQualityOnRealMySQL8(t *testing.T) {
	dsn := os.Getenv("MYSQL8_DSN")
	if dsn == "" {
		t.Skip("run through make test-mysql8")
	}
	raw, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, raw.Close()) })
	verifyRealMySQLServer(t, raw)
	source := (&execution.Module{}).MigrationSource()
	source.Prefix = "quality_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "mysql"))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, "mysql", 17))
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "mysql"), "supporting indexes can be reapplied after a partial rollback")
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, "mysql", 2))
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "mysql"))
	db := openMilestoneGORM(t, raw, "mysql")
	now := time.Now().UTC().Truncate(time.Millisecond)
	parent := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	table := source.Prefix + parent.TableName()
	require.NoError(t, db.Table(table).Create(&parent).Error)
	child := parent
	child.ID = 2
	child.ParentExecutionID = &parent.ID
	child.SourceDecisionID = new(parent.ID + 76)
	require.NoError(t, db.Table(table).Create(&child).Error)
	require.Error(t, db.Table(table).Where("id=?", child.ID).Update("source_decision_id", nil).Error)
	require.Error(t, db.Table(table).Where("id=?", child.ID).Update("parent_execution_id", nil).Error)
	require.Error(t, db.Table(table).Where("id=?", child.ID).Update("source_decision_id", 0).Error)
	require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, "mysql", 2), "durable lineage refuses rollback")
	var stored model.Execution
	require.NoError(t, db.Table(table).Where("id=?", child.ID).First(&stored).Error)
	require.Equal(t, child.SourceDecisionID, stored.SourceDecisionID)
	require.Equal(t, child.ParentExecutionID, stored.ParentExecutionID)
}

func TestExecutionPendingClaimUpgradeAndRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			raw, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, raw.Close()) })
			verifyRealMySQLServer(t, raw)
			return raw
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			raw.SetMaxOpenConns(1)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = "q_candidate_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 6))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			legacy := model.Execution{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, AssignmentID: 5, MachineID: 6, InitiatorID: 8, State: "SUCCEEDED", ResultState: "CONFIRMED", PlanJSON: "{}", PlanHash: "hash", DispatchEpoch: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			table := source.Prefix + legacy.TableName()
			require.NoError(t, db.Table(table).Omit("PendingClaimJSON", "ProgressJSON", "StrategyParentID").Create(&legacy).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Execution{}, table, "progress_json", "strategy_parent_id")
			var stored model.Execution
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).First(&stored).Error)
			require.Nil(t, stored.PendingClaimJSON, "upgrade must not fabricate candidate evidence for legacy executions")
			require.Equal(t, legacy.PlanJSON, stored.PlanJSON)
			require.True(t, at.Equal(stored.CreatedAt), "2040 timestamp retains its instant and milliseconds")
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 6), "legacy NULL evidence does not block rollback")
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).Update("pending_claim_json", "").Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 6), "empty text is not candidate evidence")
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			claim := `{"head_commit":"0123456789abcdef0123456789abcdef01234567","evidence":[{"id":"20","kind":"COMMIT","title":"Execution head commit","content_hash":"observed-commit-digest"}]}`
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).Update("pending_claim_json", claim).Error)
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 6), "refused rollback preserves actual candidate evidence")
				stored = model.Execution{}
				require.NoError(t, db.Table(table).Where("id=?", legacy.ID).First(&stored).Error)
				require.NotNil(t, stored.PendingClaimJSON)
				require.Equal(t, claim, *stored.PendingClaimJSON, "candidate bytes remain unchanged after every refused rollback")
				require.True(t, at.Equal(stored.CreatedAt))
				require.True(t, at.Equal(stored.UpdatedAt))
				require.Equal(t, 1, tableRowCount(t, raw, table))
			}
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).Update("pending_claim_json", nil).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 6), "cleared evidence permits rollback after repeated refusals")
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Execution{}, table, "progress_json", "strategy_parent_id")
			stored = model.Execution{}
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).First(&stored).Error)
			require.Nil(t, stored.PendingClaimJSON)
			require.Equal(t, legacy.PlanJSON, stored.PlanJSON)
			require.True(t, at.Equal(stored.CreatedAt))
		})
	}
}

func TestExecutionHandoverArtifactsAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&execution.Module{}).MigrationSource()
			source.Prefix = "handover_"
			// Exercise the artifact rollback guard itself before later migrations;
			// an unrelated later rollback failure must not satisfy this assertion.
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 11))
			row := model.HandoverArtifact{ID: 701, TenantID: 7, RetirementID: 31, AgentPrincipalID: 21, Source: "SYSTEM", Content: "Actual system source snapshot", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)}
			table := source.Prefix + row.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, row, table)
			db := openMilestoneGORM(t, raw, engine.dialect)
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.HandoverArtifact
			require.NoError(t, db.Table(table).Where("id=?", row.ID).First(&stored).Error)
			require.Nil(t, stored.ExecutionID)
			require.Equal(t, row.Content, stored.Content)
			require.True(t, row.CreatedAt.Equal(stored.CreatedAt))
			// MySQL's in-process engine does not enforce CHECK; the real engine is
			// included when MYSQL8_DSN is supplied, without claiming embedded parity.
			if engine.dialect != "mysql" || engine.name != "mysql" {
				require.Error(t, db.Table(table).Where("id=?", row.ID).Update("source", "AGENT").Error, "Agent output must name a genuine producer")
				require.Error(t, db.Table(table).Where("id=?", row.ID).Update("agent_principal_id", 0).Error)
			}
			for retry := 0; retry < 2; retry++ {
				require.ErrorContains(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 10), "version:11", "immutable handover documents refuse destructive rollback at their own migration")
				stored = model.HandoverArtifact{}
				require.NoError(t, db.Table(table).Where("id=?", row.ID).First(&stored).Error)
				require.Equal(t, row.Content, stored.Content)
				require.Equal(t, row.SHA256, stored.SHA256)
				require.Nil(t, stored.ExecutionID)
			}
			require.NoError(t, db.Table(table).Where("id=?", row.ID).Delete(&model.HandoverArtifact{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 10))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, row, table)
		})
	}
}
