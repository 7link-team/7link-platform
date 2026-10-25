package migration_test

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/internal/yunxiao/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestYunxiaoSchemaModelsAndIsolationAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run through make test-mysql8")
		}
		raw, e := sql.Open("mysql", dsn)
		require.NoError(t, e)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&yunxiao.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []interface{ TableName() string }{model.IngressObservation{}, model.Connection{}, model.ProjectMapping{}, model.ExternalRef{}, model.StateMapping{}, model.ImportJob{}, model.InboundEvent{}, model.SyncLog{}, model.Worklog{}, model.WorklogRevision{}, model.WorkflowConfig{}, model.IntakeChannel{}, model.Iteration{}, model.PlanningVersion{}, model.Milestone{}, model.WorkPlanning{}, model.PlanningCarryover{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, deploymentPrefix+row.TableName())
			}
			if engine.name == "mysql" {
				return
			} // This engine is a schema harness; constraints require real MySQL.
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			c := model.Connection{ID: 1, TenantID: 1, Version: 1, Provider: "YUNXIAO", Name: "Source", OrganizationID: "org", APIEndpoint: "", TokenSealed: "", TokenLast4: "", WebhookSecretSealed: "", State: "PENDING_CHECK", TokenScopes: "[]", CreatedBy: 11, UpdatedBy: 11, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+c.TableName()).Create(&c).Error)
			bad := c
			bad.ID = 2
			bad.State = "VERIFIED"
			require.Error(t, db.Table(deploymentPrefix+c.TableName()).Create(&bad).Error)
			mapping := model.ProjectMapping{ID: 3, TenantID: 1, Version: 1, ConnectionID: 1, ProjectID: 99, ExternalProjectID: "remote", ExternalProjectName: "Remote", SyncMode: "IMPORT_ONLY", FieldMasters: "{}", CodeupRepositories: "[]", TrustedPipelines: "[]", CreatedBy: 11, UpdatedBy: 11, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+mapping.TableName()).Create(&mapping).Error)
			dupe := mapping
			dupe.ID = 4
			dupe.ExternalProjectID = "other"
			require.Error(t, db.Table(deploymentPrefix+dupe.TableName()).Create(&dupe).Error)
			dupe.ProjectID = 100
			dupe.ExternalProjectID = "remote"
			require.Error(t, db.Table(deploymentPrefix+dupe.TableName()).Create(&dupe).Error)
			work := guid.ID(77)
			row := model.Worklog{ID: 900, TenantID: 1, ProjectID: 99, PrincipalID: 11, WorkItemID: &work, ActorKind: "HUMAN", Category: "DEVELOPMENT", WorkDate: "2040-01-01", Minutes: 60, Note: "Actual work", State: "DRAFT", Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+row.TableName()).Create(&row).Error)
			observation := model.IngressObservation{ID: 920, TenantID: 1, ProjectID: 99, MappingID: 3, EventID: 921, CreatedAt: now, Source: "FLOW", Detail: `{"local_acceptance":false}`, ContentHash: strings.Repeat("a", 64)}
			observations := deploymentPrefix + observation.TableName()
			require.NoError(t, db.Table(observations).Create(&observation).Error)
			duplicateObservation := observation
			duplicateObservation.ID = 922
			require.Error(t, db.Table(observations).Create(&duplicateObservation).Error)
			duplicateObservation.MappingID = 4
			duplicateObservation.Source = "UNSIGNED"
			require.Error(t, db.Table(observations).Create(&duplicateObservation).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 10))
			require.Equal(t, 1, tableRowCount(t, raw, observations))
			require.NoError(t, db.Table(observations).Where("id=920").Delete(&observation).Error)
			zero := guid.ID(0)
			row.ID = 901
			row.WorkItemID = &zero
			require.Error(t, db.Table(deploymentPrefix+row.TableName()).Create(&row).Error)
			row.ID = 902
			row.WorkItemID = nil
			require.NoError(t, db.Table(deploymentPrefix+row.TableName()).Create(&row).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 0))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestYunxiaoIntakeChannelPoliciesAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&yunxiao.Module{}).MigrationSource()
			source.Prefix = "intake_policy_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			table := source.Prefix + model.IntakeChannel{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IntakeChannel{}, table)
			if engine.name == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			row := model.IntakeChannel{ID: 100, TenantID: 1, ProjectID: 2, Kind: "EMAIL", Document: `{"enabled":false,"language":"en-US"}`, Version: 1, UpdatedBy: 11, UpdatedAt: now}
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.IntakeChannel
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).First(&stored).Error)
			require.Equal(t, row.Document, stored.Document)
			require.Nil(t, stored.CredentialHash)
			require.Nil(t, stored.ReceivingDay)
			require.Zero(t, stored.ReceivedCount)
			hash, day := strings.Repeat("a", 64), "2040-01-01"
			stored.CredentialHash, stored.ReceivingDay, stored.ReceivedCount = &hash, &day, 9
			require.NoError(t, db.Table(table).Save(&stored).Error)
			var received model.IntakeChannel
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).First(&received).Error)
			require.Equal(t, hash, *received.CredentialHash)
			require.Equal(t, day, *received.ReceivingDay)
			require.Equal(t, 9, received.ReceivedCount)
			require.True(t, now.Equal(stored.UpdatedAt), "timestamp must preserve the same instant across session timezones")
			duplicate := row
			duplicate.ID = 101
			require.Error(t, db.Table(table).Create(&duplicate).Error, "one channel policy per project and kind")
			duplicate.TenantID = 2
			require.NoError(t, db.Table(table).Create(&duplicate).Error, "another tenant remains isolated")
			bad := row
			bad.ID = 102
			bad.Kind = "IM"
			require.Error(t, db.Table(table).Create(&bad).Error)
			bad.Kind = "API"
			bad.Version = 0
			require.Error(t, db.Table(table).Create(&bad).Error)
			require.NoError(t, db.Table(table).Where("id > ?", 0).Delete(&model.IntakeChannel{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 3))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IntakeChannel{}, table)
		})
	}
}

func TestYunxiaoAutomationRunSnapshotsAndTriggerUniquenessAcrossEngines(t *testing.T) {
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
			source := (&yunxiao.Module{}).MigrationSource()
			source.Prefix = "automation_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []interface{ TableName() string }{model.AutomationRule{}, model.AutomationRun{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, source.Prefix+row.TableName())
			}
			if engine.name == "mysql" {
				return
			} // Constraint enforcement is checked by real MySQL 8.
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			rule := model.AutomationRule{ID: 10, TenantID: 1, ProjectID: 99, CreatedBy: 11, Name: "Original rule", Version: 1, Enabled: true, CreatedAt: now, UpdatedAt: now}
			rules := source.Prefix + rule.TableName()
			require.NoError(t, db.Table(rules).Create(&rule).Error)
			run := model.AutomationRun{ID: 100, TenantID: 1, ProjectID: 99, RuleID: rule.ID, RuleVersion: 1, RuleName: rule.Name, CreatedBy: rule.CreatedBy, TriggerEventID: 700, WorkItemID: 88, Outcome: "FAILED", Error: "ACTION_FAILED", DurationMS: 10, CreatedAt: now}
			runs := source.Prefix + run.TableName()
			require.NoError(t, db.Table(runs).Create(&run).Error)
			// A replay cannot replace an immutable failure with a different outcome,
			// even after the rule itself changes version or is disabled.
			require.NoError(t, db.Table(rules).Where("id = ?", rule.ID).Updates(map[string]any{"name": "Edited rule", "version": 2, "enabled": false}).Error)
			duplicate := run
			duplicate.ID = 101
			duplicate.Outcome = "SUCCEEDED"
			duplicate.RuleVersion = 2
			require.Error(t, db.Table(runs).Create(&duplicate).Error)
			var stored model.AutomationRun
			require.NoError(t, db.Table(runs).Where("id = ?", run.ID).First(&stored).Error)
			require.Equal(t, "FAILED", stored.Outcome)
			require.Equal(t, "Original rule", stored.RuleName)
			require.Equal(t, 1, stored.RuleVersion)
			require.Equal(t, guid.ID(11), stored.CreatedBy)
			require.True(t, now.Equal(stored.CreatedAt))
			// Tenant, rule, trigger and work item all participate in receipt identity.
			for i, change := range []func(*model.AutomationRun){func(r *model.AutomationRun) { r.TenantID = 2 }, func(r *model.AutomationRun) { r.RuleID = 11 }, func(r *model.AutomationRun) { r.TriggerEventID = 701 }, func(r *model.AutomationRun) { r.WorkItemID = 89 }} {
				other := run
				other.ID = guid.ID(110 + i)
				change(&other)
				require.NoError(t, db.Table(runs).Create(&other).Error)
			}
			bad := run
			bad.ID = 120
			bad.TriggerEventID = 702
			bad.Outcome = "PENDING"
			require.Error(t, db.Table(runs).Create(&bad).Error)
			bad.Outcome = "FAILED"
			bad.DurationMS = -1
			require.Error(t, db.Table(runs).Create(&bad).Error)
			badRule := rule
			badRule.ID = 12
			badRule.Version = 0
			require.Error(t, db.Table(rules).Create(&badRule).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 5))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestYunxiaoEffortConfirmationUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&yunxiao.Module{}).MigrationSource()
			source.Prefix = "effort_upgrade_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 9))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			// Old storage does not yet have the three new columns.
			table := source.Prefix + "delivery_worklogs"
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 900, "tenant_id": 1, "project_id": 99, "principal_id": 11, "actor_kind": "HUMAN", "category": "TEST", "work_date": "2040-01-01", "minutes": 45, "cost_micros": 0, "note": "Legacy approved fact", "state": "APPROVED", "version": 3, "created_at": at, "updated_at": at}).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 10))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Worklog{}, table)
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.WorklogRevision{}, source.Prefix+"delivery_worklog_revisions")
			var row model.Worklog
			require.NoError(t, db.Table(table).Where("id=900").Take(&row).Error)
			require.Nil(t, row.Timezone)
			require.Nil(t, row.ConfirmedBy)
			require.Nil(t, row.ConfirmedAt)
			require.Equal(t, "APPROVED", row.State)
			require.EqualValues(t, 45, row.Minutes)
			revisions := source.Prefix + "delivery_worklog_revisions"
			revision := model.WorklogRevision{ID: 901, TenantID: 1, ProjectID: 99, WorklogID: 900, Version: 3, Snapshot: `{"minutes":45}`, Action: "LEGACY_OBSERVED", Reason: "", RecordedAt: at}
			require.NoError(t, db.Table(revisions).Create(&revision).Error)
			if engine.name != "mysql" {
				duplicate := revision
				duplicate.ID = 902
				require.Error(t, db.Table(revisions).Create(&duplicate).Error)
				duplicate.ID = 903
				duplicate.Version = 4
				duplicate.ProjectID = 100
				require.Error(t, db.Table(revisions).Create(&duplicate).Error)
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 9))
				require.Equal(t, 1, tableRowCount(t, raw, revisions))
			}
			require.NoError(t, db.Table(revisions).Where("id=901").Delete(&model.WorklogRevision{}).Error)
			require.NoError(t, db.Table(table).Where("id=900").Update("timezone", "UTC").Error)
			if engine.name != "mysql" {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 9))
			}
			require.NoError(t, db.Table(table).Where("id=900").Update("timezone", nil).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 9))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 10))
			row = model.Worklog{}
			require.NoError(t, db.Table(table).Where("id=900").Take(&row).Error)
			require.Nil(t, row.ConfirmedAt)
			require.EqualValues(t, 45, row.Minutes)
		})
	}
}

func TestYunxiaoIngressObservationUpgradeAndRollbackGuardAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&yunxiao.Module{}).MigrationSource()
			source.Prefix = "ingress_upgrade_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 10))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 11))
			table := source.Prefix + model.IngressObservation{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IngressObservation{}, table)
			if engine.name != "mysql" {
				db := openMilestoneGORM(t, raw, engine.dialect)
				row := model.IngressObservation{ID: 1, TenantID: 1, ProjectID: 99, MappingID: 100, EventID: 101, CreatedAt: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), Source: "FLOW", Detail: `{"local_acceptance":false}`, ContentHash: strings.Repeat("a", 64)}
				require.NoError(t, db.Table(table).Create(&row).Error)
				duplicate := row
				duplicate.ID = 2
				require.Error(t, db.Table(table).Create(&duplicate).Error)
				duplicate.ID = 3
				duplicate.MappingID = 102
				duplicate.Source = "UNTRUSTED"
				require.Error(t, db.Table(table).Create(&duplicate).Error)
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 10))
				require.Equal(t, 1, tableRowCount(t, raw, table))
				require.NoError(t, db.Table(table).Where("id=1").Delete(&row).Error)
			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 10))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 11))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.IngressObservation{}, table)
		})
	}
}
