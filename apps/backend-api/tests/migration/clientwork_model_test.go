package migration_test

import (
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/clientwork/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientworkSchemasAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			models := []struct {
				row   tableModel
				table string
			}{{model.Sheet{}, "sheets"}, {model.SheetRevision{}, "sheet_revisions"}, {model.Signoff{}, "signoffs"}, {model.ChangeRequest{}, "change_requests"}, {model.ClientIssue{}, "issues"}, {model.IssueReply{}, "issue_replies"}, {model.DeliveryPackage{}, "packages"}, {model.PackageReceipt{}, "package_receipts"}, {model.Preview{}, "previews"}, {model.Annotation{}, "annotations"}, {model.TestCase{}, "test_cases"}, {model.TestPlan{}, "test_plans"}, {model.TestRun{}, "test_runs"}, {model.Tolerance{}, "tolerances"}, {model.ToleranceException{}, "exceptions"}, {model.PreviewShare{}, "preview_shares"}, {model.PackageTimeline{}, "package_timeline"}, {model.DispatchProposal{}, "dispatch_proposals"}, {model.CommandDecision{}, "command_decisions"}}
			for _, m := range models {
				assertModelMatchesNamedTable(t, raw, engine.dialect, m.row, deploymentPrefix+"clientwork_"+m.table)
			}
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.DispatchAction{}, deploymentPrefix+"clientwork_dispatch_actions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ClarificationRound{}, deploymentPrefix+"clientwork_clarification_rounds")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ClarificationDraft{}, deploymentPrefix+"clientwork_clarification_drafts")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IntakeSuggestion{}, deploymentPrefix+"clientwork_intake_suggestions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IntakeThread{}, deploymentPrefix+"clientwork_intake_threads")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReworkDisposition{}, deploymentPrefix+"clientwork_rework_dispositions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.HiddenCaseArchive{}, deploymentPrefix+"clientwork_hidden_case_archive")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.HiddenVerificationReceipt{}, deploymentPrefix+"clientwork_hidden_verification_receipts")
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestClientworkSavedTestReportSchemaAndRollbackAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = "saved_report_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.TestReport{}, source.Prefix+"clientwork_test_reports")
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			row := model.TestReport{TenantID: 1, ID: 10, ProjectID: 2, ActorID: 3, Title: "Frozen report", Snapshot: "{}", State: "PENDING", JobID: 4, CreatedAt: at}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			for _, updates := range []map[string]any{{"state": "BUILT"}, {"job_id": 0}, {"state": "UNKNOWN"}, {"content_hash": strings.Repeat("a", 64)}} {
				require.Error(t, db.Table(table).Where("id=10").Updates(updates).Error)
			}
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 31))
			require.Equal(t, 1, tableRowCount(t, raw, table))
			require.NoError(t, db.Table(table).Where("id=10").Updates(map[string]any{"state": "BUILT", "binding_id": 11, "file_id": 12, "version_id": 13, "content_hash": strings.Repeat("a", 64), "built_at": at}).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 31))
			require.Equal(t, 1, tableRowCount(t, raw, table))
			require.NoError(t, db.Table(table).Where("id=10").Delete(&row).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 31))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestClientworkHiddenRollbackAcrossEngines(t *testing.T) {
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
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			if engine.name == "mysql8" {
				source.Prefix = "fixture_hidden_"
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 17))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.TestCase{}).TableName()
			now := time.Now().UTC().Truncate(time.Millisecond)
			public := model.TestCase{TenantID: 7, ID: 1, ProjectID: 3, Title: "Public case", Steps: "Public steps", Expected: "Public result", WorkItemID: 5, Version: 1, ActorID: 4, CreatedAt: now, UpdatedAt: now}
			// Freeze the pre-18 fixture columns; later model fields do not exist yet.
			legacyColumns := []string{"tenant_id", "id", "project_id", "title", "steps", "expected", "work_item_id", "defect_id", "version", "actor_id", "created_at", "updated_at"}
			require.NoError(t, db.Table(table).Select(legacyColumns).Create(&public).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 18))
			var stored model.TestCase
			require.NoError(t, db.Table(table).Where("tenant_id=? AND id=?", public.TenantID, public.ID).First(&stored).Error)
			require.False(t, stored.IsHiddenAcceptance, "legacy cases remain public after upgrade")
			require.Equal(t, public.Title, stored.Title)
			require.Equal(t, public.Steps, stored.Steps)
			require.Equal(t, public.Expected, stored.Expected)

			hidden := public
			hidden.ID, hidden.IsHiddenAcceptance = 2, true
			hidden.Title, hidden.Steps, hidden.Expected = "Hidden case", "Confidential steps", "Confidential result"
			require.NoError(t, db.Table(table).Select(append(legacyColumns, "is_hidden_acceptance")).Create(&hidden).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 17), "rollback must not declassify hidden cases")
			stored = model.TestCase{}
			require.NoError(t, db.Table(table).Where("tenant_id=? AND id=?", hidden.TenantID, hidden.ID).First(&stored).Error)
			require.True(t, stored.IsHiddenAcceptance, "failed rollback retains the confidentiality flag")
			require.Equal(t, hidden.Title, stored.Title)
			require.Equal(t, hidden.Steps, stored.Steps)
			require.Equal(t, hidden.Expected, stored.Expected)
			require.Equal(t, hidden.Version, stored.Version)
			require.Equal(t, 2, tableRowCount(t, raw, table), "refused rollback preserves both records")

			require.NoError(t, db.Table(table).Where("tenant_id=? AND id=?", hidden.TenantID, hidden.ID).Delete(&model.TestCase{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 17), "public-only data permits rollback after a refused attempt")
			require.False(t, db.Migrator().HasColumn(table, "is_hidden_acceptance"), "successful rollback removes the new column")
			stored = model.TestCase{}
			require.NoError(t, db.Table(table).Where("tenant_id=? AND id=?", public.TenantID, public.ID).First(&stored).Error)
			require.Equal(t, public.Title, stored.Title)
			require.Equal(t, public.Steps, stored.Steps)
			require.Equal(t, public.Expected, stored.Expected)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 18), "hidden protection can be reapplied after rollback")
			stored = model.TestCase{}
			require.NoError(t, db.Table(table).Where("tenant_id=? AND id=?", public.TenantID, public.ID).First(&stored).Error)
			require.False(t, stored.IsHiddenAcceptance)
			require.Equal(t, public.Title, stored.Title)
			require.Equal(t, public.Steps, stored.Steps)
			require.Equal(t, public.Expected, stored.Expected)
			require.Equal(t, 1, tableRowCount(t, raw, table))
		})
	}
}

func TestClientworkClarificationClosureAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run through the isolated MySQL8 gate")
		}
		raw, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			if engine.name == "mysql8" {
				source.Prefix = "regression_closure_"
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 18))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.ClarificationRound{}).TableName()
			at := time.Now().UTC().Truncate(time.Millisecond)
			legacy := model.ClarificationRound{ID: 1, TenantID: 7, ProjectID: 3, IntakeID: 5, IntakeVersion: 1, RecipientID: 4, State: "ANSWERED", Questions: "[]", Version: 2, CreatedBy: 2, CreatedAt: at.Add(-time.Hour), UpdatedAt: at}
			// Version 18 predates both closure metadata and the timing columns.
			legacyColumns := []string{"id", "tenant_id", "project_id", "intake_id", "intake_version", "recipient_id", "state", "questions", "version", "created_by", "created_at", "updated_at"}
			require.NoError(t, db.Table(table).Select(legacyColumns).Create(&legacy).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 19))
			var stored model.ClarificationRound
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).Take(&stored).Error)
			require.NotNil(t, stored.ClosedAt)
			require.True(t, stored.ClosedAt.Equal(at), "backfill uses the recorded completion time")
			require.Equal(t, "ALL_ANSWERED", *stored.CloseReason)
			require.Nil(t, stored.CloseNote)
			for _, changes := range []map[string]any{
				{"close_reason": nil},
				{"state": "OPEN"},
				{"state": "CANCELLED", "close_reason": "CANCELLED_BY_TEAM", "close_note": nil},
				{"state": "CANCELLED", "close_reason": "CANCELLED_BY_TEAM", "close_note": ""},
			} {
				require.Error(t, db.Table(table).Where("id=?", legacy.ID).Updates(changes).Error, "invalid closure metadata must fail at storage boundary")
			}
			reason, note := "CANCELLED_BY_TEAM", "No longer needed"
			cancelled := legacy
			cancelled.ID, cancelled.State, cancelled.ClosedAt, cancelled.CloseReason, cancelled.CloseNote = 2, "CANCELLED", &at, &reason, &note
			require.NoError(t, db.Table(table).Select(append(legacyColumns, "closed_at", "close_reason", "close_note")).Create(&cancelled).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 18), "rollback cannot rewrite cancellation history")
			stored = model.ClarificationRound{}
			require.NoError(t, db.Table(table).Where("id=?", cancelled.ID).Take(&stored).Error)
			require.Equal(t, cancelled.State, stored.State)
			require.Equal(t, note, *stored.CloseNote)
			require.NoError(t, db.Table(table).Where("id=?", cancelled.ID).Delete(&model.ClarificationRound{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 18))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 19))
			require.Equal(t, 1, tableRowCount(t, raw, table))
		})
	}
}

func TestClientworkHiddenArchiveRollbackAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 20))
			db := openMilestoneGORM(t, raw, engine.dialect)
			archive := source.Prefix + (model.HiddenCaseArchive{}).TableName()
			receipts := source.Prefix + (model.HiddenVerificationReceipt{}).TableName()
			at := time.Now().UTC().Truncate(time.Millisecond)
			require.NoError(t, db.Table(archive).Create(&model.HiddenCaseArchive{TenantID: 7, ProjectID: 3, ContentHash: strings.Repeat("a", 64), CaseID: 9, CaseVersion: 1, Content: []byte("private bytes"), CreatedAt: at}).Error)
			for i := 0; i < 2; i++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 19))
				require.Equal(t, 1, tableRowCount(t, raw, archive))
			}
			require.NoError(t, db.Table(archive).Where("tenant_id=?", 7).Delete(&model.HiddenCaseArchive{}).Error)
			require.NoError(t, db.Table(receipts).Create(&model.HiddenVerificationReceipt{TenantID: 7, ID: 1, ProjectID: 3, WorkItemID: 5, ResultID: 6, SuiteHash: strings.Repeat("b", 64), HeadCommit: strings.Repeat("c", 40), VerifierPrincipalID: 11, Model: "review-model", Family: "independent", Verdict: "FAILED", Reason: "VERIFIER_COMMAND_COMPLETED", InputHash: "", OutputHash: "", ReceiptHash: strings.Repeat("d", 64), CreatedAt: at}).Error)
			for i := 0; i < 2; i++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 19))
				require.Equal(t, 1, tableRowCount(t, raw, receipts))
			}
			require.NoError(t, db.Table(receipts).Where("tenant_id=?", 7).Delete(&model.HiddenVerificationReceipt{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 19))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 20))
		})
	}
}

func TestClientworkIntakeThreadAndSuggestionSchemas(t *testing.T) {
	engines := []struct {
		name, dialect string
		open          func(*testing.T) *sql.DB
	}{
		{"sqlite", "sqlite", testdb.OpenSQLite}, {"mysql", "mysql", testdb.OpenMySQL}, {"postgres", "postgres", testdb.OpenPostgres},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = []struct {
			name, dialect string
			open          func(*testing.T) *sql.DB
		}{{"mysql8", "mysql", func(t *testing.T) *sql.DB {
			db, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			var version string
			require.NoError(t, db.QueryRow("SELECT VERSION()").Scan(&version))
			require.True(t, strings.HasPrefix(version, "8."), version)
			return db
		}}}
	}
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			db := e.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = "intake_test_"
			require.NoError(t, goosex.Migrate(t.Context(), db, source, e.dialect))
			thread := `INSERT INTO intake_test_clientwork_intake_threads (tenant_id,intake_id,project_id,channel_id,root_message_id,recipient_id,created_by,created_at) VALUES (1,50,3,70,71,4,2,'2026-10-21 22:00:00')`
			_, err := db.Exec(thread)
			require.NoError(t, err)
			_, err = db.Exec(thread)
			require.Error(t, err, "a source can never be silently relinked")
			_, err = db.Exec(strings.Replace(thread, "(1,50,3,70,71,4,2,", "(2,50,3,70,71,4,2,", 1))
			require.NoError(t, err, "tenant keys remain isolated")
			_, err = db.Exec(strings.Replace(thread, "(1,50,3,70,71,4,2,", "(1,51,3,70,71,0,2,", 1))
			require.Error(t, err, "invalid contact identity fails closed")
			suggestion := `INSERT INTO intake_test_clientwork_intake_suggestions (id,tenant_id,project_id,intake_id,intake_version,proposal,actor_kind,actor_id,created_at) VALUES (100,1,3,50,1,'{}','AGENT',25,'2026-10-21 22:00:00')`
			_, err = db.Exec(suggestion)
			require.NoError(t, err)
			_, err = db.Exec(suggestion)
			require.Error(t, err, "immutable suggestion identity cannot be overwritten")
			_, err = db.Exec(strings.Replace(strings.Replace(suggestion, "(100,", "(101,", 1), "'AGENT'", "'RUNNER'", 1))
			require.Error(t, err, "no fabricated runtime actor")
			_, err = db.Exec(strings.Replace(suggestion, "(100,1,3,50,1,", "(101,1,3,50,0,", 1))
			require.Error(t, err, "source version is mandatory")
			if e.name != "mysql" { // The embedded memory engine does not implement transaction rollback. Real MySQL8 below must prove it.
				tx, err := db.Begin()
				require.NoError(t, err)
				_, err = tx.Exec(strings.Replace(suggestion, "(100,", "(102,", 1))
				require.NoError(t, err)
				require.NoError(t, tx.Rollback())
			}
			var n int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM intake_test_clientwork_intake_suggestions").Scan(&n))
			require.Equal(t, 1, n, "outer audit failure can roll back the persisted suggestion")
			require.NoError(t, goosex.Migrate(t.Context(), db, source, e.dialect))
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM intake_test_clientwork_intake_threads").Scan(&n))
			require.Equal(t, 2, n, "migration rerun preserves exact source associations")
		})
	}
}

func TestClientworkTestCaseImportMetadataAcrossEngines(t *testing.T) {
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
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = "case_import_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.TestCase{}, source.Prefix+model.TestCase{}.TableName())
			db := openMilestoneGORM(t, raw, engine.dialect)
			old := model.TestCase{TenantID: 1, ID: 100, ProjectID: 3, Title: "Old", Steps: "Run", Expected: "Done", WorkItemID: 11, Version: 1, ActorID: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			require.NoError(t, db.Table(source.Prefix+old.TableName()).Create(&old).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 26))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var stored model.TestCase
			require.NoError(t, db.Table(source.Prefix+old.TableName()).First(&stored).Error)
			require.Equal(t, "P2", stored.Priority)
			require.False(t, stored.Unclassified)
			require.Nil(t, stored.RequirementID)
			require.NoError(t, db.Table(source.Prefix+old.TableName()).Where("id=?", 100).Updates(map[string]any{"requirement_id": 10, "criterion_source": "unmatched", "preconditions": "Session ready", "priority": "P1", "unclassified": true}).Error)
			require.NoError(t, db.Table(source.Prefix+old.TableName()).First(&stored).Error)
			require.Equal(t, "Session ready", stored.Preconditions)
			require.True(t, stored.Unclassified)
			require.Error(t, db.Table(source.Prefix+old.TableName()).Where("id=?", 100).Update("priority", "P9").Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 26), "cannot discard imported metadata")
			require.NoError(t, db.Table(source.Prefix+old.TableName()).Where("id=?", 100).Delete(&model.TestCase{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 26), "empty retry after refusal remains possible")
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

// Recorded receipts may never lose their recorder/evidence through downgrade.
func TestClientworkRecordedAcceptanceRollbackAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := deploymentPrefix + "clientwork_package_receipts"
			at := time.Now().UTC().Truncate(time.Millisecond)
			require.NoError(t, db.Table(table).Create(map[string]any{"tenant_id": 1, "id": 901, "project_id": 3, "package_id": 900, "content_hash": strings.Repeat("a", 64), "decision": "ACCEPT", "reason": "Actual client signature", "actor_id": 4, "created_at": at}).Error)
			var row model.PackageReceipt
			require.NoError(t, db.Table(table).Where("id=901").Take(&row).Error)
			require.Equal(t, "PORTAL", row.Source)
			require.Nil(t, row.RecordedBy)
			require.NoError(t, db.Table(table).Where("id=901").Updates(map[string]any{"source": "RECORDED", "recorded_by": 2, "evidence_binding_id": 902, "evidence_json": "{}"}).Error)
			for range 2 {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 27))
				var saved model.PackageReceipt
				require.NoError(t, db.Table(table).Where("id=901").Take(&saved).Error)
				require.Equal(t, "RECORDED", saved.Source)
				require.EqualValues(t, 2, *saved.RecordedBy)
				require.EqualValues(t, 902, *saved.EvidenceBindingID)
			}
			require.NoError(t, db.Table(table).Where("id=901").Updates(map[string]any{"source": "PORTAL", "recorded_by": nil, "evidence_binding_id": nil, "evidence_json": nil}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 27))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestClientworkReleaseNoteSchemasAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = "release_note_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReleaseNoteDraft{}, source.Prefix+(model.ReleaseNoteDraft{}).TableName())
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Now().UTC().Truncate(time.Millisecond)
			pkg := model.DeliveryPackage{TenantID: 1, ID: 900, ProjectID: 3, Title: "Release", ReleaseNotes: "Notes", Manifest: "{}", ContentHash: strings.Repeat("a", 64), State: "DRAFT", RecipientID: 4, ActorID: 2, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+pkg.TableName()).Create(&pkg).Error)
			draft := model.ReleaseNoteDraft{TenantID: 1, ID: 901, ProjectID: 3, PackageID: 900, SourceFingerprint: strings.Repeat("b", 64), SourceSnapshot: "{}", RequestedBy: 2, DrafterPrincipalID: 8, Note: "Summarize", State: "REQUESTED", Version: 1, CreatedAt: at, UpdatedAt: at}
			table := source.Prefix + draft.TableName()
			require.NoError(t, db.Table(table).Create(&draft).Error)
			for range 2 {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 28))
				require.Equal(t, 1, tableRowCount(t, raw, table))
			}
			require.NoError(t, db.Table(table).Where("id=901").Delete(&model.ReleaseNoteDraft{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 28))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestClientworkAcceptanceRemindersAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = "acceptance_reminder_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.AcceptanceReminder{}, source.Prefix+"clientwork_acceptance_reminders")
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			pkg := model.DeliveryPackage{TenantID: 1, ID: 10, ProjectID: 2, Title: "Pinned release", ReleaseNotes: "Recorded notes", Manifest: "[]", ContentHash: strings.Repeat("a", 64), State: "PUBLISHED", RecipientID: 4, ActorID: 3, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+pkg.TableName()).Create(&pkg).Error)
			row := model.AcceptanceReminder{TenantID: 1, ID: 20, ProjectID: 2, PackageID: 10, PackageVersion: 1, ContentHash: pkg.ContentHash, RecipientID: 4, Day: 3, Kind: "REMINDER", PublishedAt: at, DueAt: at.Add(72 * time.Hour)}
			require.NoError(t, db.Table(source.Prefix+row.TableName()).Create(&row).Error)
			duplicate := row
			duplicate.ID = 21
			require.Error(t, db.Table(source.Prefix+row.TableName()).Create(&duplicate).Error)
			for _, change := range []string{"day_no=4", "kind='ESCALATION'", "package_id=99", "package_version=0", "project_id=99"} {
				_, e := raw.ExecContext(t.Context(), "UPDATE "+source.Prefix+row.TableName()+" SET "+change+" WHERE id=20")
				require.Error(t, e, change)
			}
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 29))
			require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+row.TableName()))
		})
	}
}
