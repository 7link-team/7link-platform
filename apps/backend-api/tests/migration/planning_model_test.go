package migration_test

import (
	"7link.com/platform/internal/planning"
	"7link.com/platform/internal/planning/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestPlanningSchemasAcrossEngines(t *testing.T) {
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
			s := (&planning.Module{}).MigrationSource()
			s.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, s, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Goal{}, deploymentPrefix+"planning_goals")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.GoalSnapshot{}, deploymentPrefix+"planning_goal_snapshots")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Plan{}, deploymentPrefix+"planning_plans")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Report{}, deploymentPrefix+"planning_reports")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReportCheck{}, deploymentPrefix+"planning_report_checks")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReportSubmission{}, deploymentPrefix+"planning_report_submissions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Settings{}, deploymentPrefix+"planning_settings")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.LoopContract{}, deploymentPrefix+"planning_loop_contracts")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.LoopRun{}, deploymentPrefix+"planning_loop_runs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Contributor{}, deploymentPrefix+"planning_goal_contributors")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.HeavyRun{}, deploymentPrefix+"planning_heavy_runs")
			// Real deadline values must survive beyond the MySQL TIMESTAMP
			// horizon; permissive schema parsers alone cannot prove this.
			db := openMilestoneGORM(t, raw, engine.dialect)
			due := time.Date(2040, 1, 9, 12, 34, 56, 123000000, time.UTC)

			plan := model.Plan{TenantID: 1, ID: 301, ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2040-01-01", State: "SUBMITTED", Content: "{}", ContentHash: "hash", Version: 2, CreatedBy: 2, CreatedAt: due, UpdatedAt: due, SubmittedAt: &due, UnconfirmedAt: &due}
			require.NoError(t, db.Table(deploymentPrefix+plan.TableName()).Create(&plan).Error)
			var storedPlan model.Plan
			require.NoError(t, db.Table(deploymentPrefix+plan.TableName()).First(&storedPlan).Error)
			require.NotNil(t, storedPlan.SubmittedAt)
			require.True(t, due.Equal(*storedPlan.SubmittedAt))
			require.NotNil(t, storedPlan.UnconfirmedAt)
			require.True(t, due.Equal(*storedPlan.UnconfirmedAt))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, s, engine.dialect, 11))
			require.NoError(t, goosex.Migrate(t.Context(), raw, s, engine.dialect))
			storedPlan = model.Plan{}
			require.NoError(t, db.Table(deploymentPrefix+plan.TableName()).First(&storedPlan).Error)
			require.Equal(t, "SUBMITTED", storedPlan.State, "timeout marker must not replace PlanState")
			require.NotNil(t, storedPlan.SubmittedAt)
			require.True(t, due.Equal(*storedPlan.SubmittedAt), "legacy submission uses the previous available clock")
			require.Nil(t, storedPlan.UnconfirmedAt, "a migration never invents a completed timeout scan")
			require.NoError(t, db.Table(deploymentPrefix+plan.TableName()).Where("tenant_id=?", 1).Delete(&model.Plan{}).Error)
			goal := model.Goal{TenantID: 1, ID: 101, ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2040-01-01", State: "ACTIVE", Content: "{}", ContentHash: "hash", Version: 1, CreatedBy: 2, CreatedAt: due, UpdatedAt: due}
			require.NoError(t, db.Table(deploymentPrefix+"planning_goals").Create(&goal).Error)
			snapshot := model.GoalSnapshot{TenantID: 1, GoalID: goal.ID, Version: 1, Content: "{}", ContentHash: "hash", State: "ACTIVE", ObservedAt: due}
			require.NoError(t, db.Table(deploymentPrefix+snapshot.TableName()).Create(&snapshot).Error)
			require.Error(t, db.Table(deploymentPrefix+snapshot.TableName()).Create(&snapshot).Error, "goal version snapshot cannot be overwritten")
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, s, engine.dialect, 10), "immutable goal history blocks destructive downgrade")
			require.NoError(t, db.Table(deploymentPrefix+snapshot.TableName()).Where("tenant_id=?", 1).Delete(&model.GoalSnapshot{}).Error)

			contributor := model.Contributor{TenantID: 1, ID: 201, GoalID: 101, KR: "delivery", PrincipalID: 3, CreatedBy: 2, CreatedAt: due}
			require.NoError(t, db.Table(deploymentPrefix+"planning_goal_contributors").Create(&contributor).Error)
			var storedContributor model.Contributor
			require.NoError(t, db.Table(deploymentPrefix+"planning_goal_contributors").Where("tenant_id=? AND id=?", 1, 201).First(&storedContributor).Error)
			require.True(t, due.Equal(storedContributor.CreatedAt))
			contributor.ID = 202
			require.Error(t, db.Table(deploymentPrefix+"planning_goal_contributors").Create(&contributor).Error, "one assignment per goal/key-result/principal")
			contributor.PrincipalID = 4
			contributor.KR = ""
			require.Error(t, db.Table(deploymentPrefix+"planning_goal_contributors").Create(&contributor).Error, "empty KR rejected by each dialect")
			report := model.Report{TenantID: 1, ID: 1, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2040-01-02", State: "DRAFT", Content: "{}", ContentHash: "hash", Version: 1, CreatedBy: 2, CreatedAt: due, UpdatedAt: due, Facts: "{}", DueAt: &due}
			require.NoError(t, db.Table(deploymentPrefix+"planning_reports").Create(&report).Error)
			check := model.ReportCheck{TenantID: 1, ID: 601, ReportID: report.ID, ReportVersion: 1, Result: "BLOCKED", CheckedAt: due}
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_checks").Create(&check).Error)
			var savedCheck model.ReportCheck
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_checks").First(&savedCheck).Error)
			require.Equal(t, "BLOCKED", savedCheck.Result)
			require.True(t, due.Equal(savedCheck.CheckedAt))
			check.ID = 602
			check.Result = "UNKNOWN"
			require.Error(t, db.Table(deploymentPrefix+"planning_report_checks").Create(&check).Error, "only actual check result codes allowed")
			check.Result = "PASS"
			check.ReportVersion = 0
			require.Error(t, db.Table(deploymentPrefix+"planning_report_checks").Create(&check).Error, "check revision must be positive")
			check.ReportVersion = 1
			check.ReportID = 999
			require.Error(t, db.Table(deploymentPrefix+"planning_report_checks").Create(&check).Error, "check requires its actual report")
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_checks").Where("tenant_id=?", 1).Delete(&model.ReportCheck{}).Error)
			receipt := model.ReportSubmission{TenantID: 1, ID: 501, ReportID: report.ID, ReportVersion: 2, SubmittedAt: due, DueAt: &due}
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_submissions").Create(&receipt).Error)
			var storedReceipt model.ReportSubmission
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_submissions").First(&storedReceipt).Error)
			require.True(t, due.Equal(storedReceipt.SubmittedAt))
			require.NotNil(t, storedReceipt.DueAt)
			require.True(t, due.Equal(*storedReceipt.DueAt))
			receipt.ID = 502
			require.Error(t, db.Table(deploymentPrefix+"planning_report_submissions").Create(&receipt).Error, "one immutable receipt per report version")
			receipt.ReportVersion = 1
			require.Error(t, db.Table(deploymentPrefix+"planning_report_submissions").Create(&receipt).Error, "submission must follow an existing report version")
			require.NoError(t, db.Table(deploymentPrefix+"planning_report_submissions").Where("tenant_id=?", 1).Delete(&model.ReportSubmission{}).Error)
			var read model.Report
			require.NoError(t, db.Table(deploymentPrefix+"planning_reports").Where("tenant_id = ? AND id = ?", 1, 1).First(&read).Error)
			require.NotNil(t, read.DueAt)
			require.True(t, due.Equal(*read.DueAt))
			require.NoError(t, db.Table(deploymentPrefix+"planning_reports").Where("tenant_id = ? AND id = ?", 1, 1).Update("due_at", nil).Error)
			read = model.Report{}
			require.NoError(t, db.Table(deploymentPrefix+"planning_reports").Where("tenant_id = ? AND id = ?", 1, 1).First(&read).Error)
			require.Nil(t, read.DueAt)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, s, engine.dialect, 0))
			require.NoError(t, goosex.Migrate(t.Context(), raw, s, engine.dialect))
		})
	}
}
