package migration_test

import (
	"7link.com/platform/internal/templates"
	"7link.com/platform/internal/templates/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTemplatesLifecycleSchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&templates.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.LifecycleReceipt{}, deploymentPrefix+"templates_lifecycle_receipts")
			if engine.name == "mysql" {
				return
			} // schema harness, not constraint evidence
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			version := model.Version{ID: 1, TenantID: 1, Key: "lifecycle", Semver: "1.0.0", State: "PUBLISHED", Definition: "{}", SHA256: "hash", Version: 1, CreatedBy: 11, UpdatedBy: 11, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"templates_versions").Create(&version).Error)
			row := model.LifecycleReceipt{ID: 2, TenantID: 1, TemplateVersionID: 1, ObjectKey: "object", SourceKey: "source", EventType: "provider.state_changed", State: "UNAVAILABLE", Payload: "{}", RequestHash: "hash", CreatedBy: 11, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"templates_lifecycle_receipts").Create(&row).Error)
			row.ID = 3
			require.Error(t, db.Table(deploymentPrefix+"templates_lifecycle_receipts").Create(&row).Error, "same tenant/source result cannot be recorded twice")
			row.SourceKey = "other-source"
			row.TemplateVersionID = 999
			require.Error(t, db.Table(deploymentPrefix+"templates_lifecycle_receipts").Create(&row).Error, "observations require an actual immutable template version")
		})
	}
}

func TestTemplatesSchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&templates.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Version{}, deploymentPrefix+"templates_versions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Binding{}, deploymentPrefix+"templates_project_bindings")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.StudioRecord{}, deploymentPrefix+"templates_studio_records")
			for _, row := range []tableModel{model.CalibrationSample{}, model.Calibration{}, model.CalibrationRun{}, model.EvalCase{}, model.EvalRun{}, model.TemplateProvider{}, model.TemplateProviderBinding{}, model.ProviderGeneration{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, deploymentPrefix+row.TableName())
			}
			verifyTemplatesEvaluationRollback(t, raw, engine.dialect)
			if engine.name == "mysql" {
				return
			} // in-memory MySQL is a schema harness, not constraint evidence.
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			version := model.Version{ID: 1, TenantID: 1, Key: "software", Semver: "1.0.0", State: "PUBLISHED", Definition: "{}", SHA256: "hash", Version: 1, CreatedBy: 11, UpdatedBy: 11, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"templates_versions").Create(&version).Error)
			duplicate := version
			duplicate.ID = 2
			require.Error(t, db.Table(deploymentPrefix+"templates_versions").Create(&duplicate).Error)
			invalid := version
			invalid.ID = 3
			invalid.Key = "invalid"
			invalid.State = "EXECUTED"
			require.Error(t, db.Table(deploymentPrefix+"templates_versions").Create(&invalid).Error)
			binding := model.Binding{ID: 4, TenantID: 1, ProjectID: 99, TemplateVersionID: 1, SHA256: "hash", ConstitutionID: 8, Runs: "[]", CreatedBy: 11, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"templates_project_bindings").Create(&binding).Error)
			binding.ID = 5
			require.Error(t, db.Table(deploymentPrefix+"templates_project_bindings").Create(&binding).Error)
			studio := model.StudioRecord{ID: 6, TenantID: 1, BindingID: 4, Domain: "VIDEO", Kind: "SHOT", State: "OPEN", Payload: "{}", Version: 1, CreatedBy: 11, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"templates_studio_records").Create(&studio).Error)
			invalidStudio := studio
			invalidStudio.ID = 7
			invalidStudio.Domain = "UNKNOWN"
			require.Error(t, db.Table(deploymentPrefix+"templates_studio_records").Create(&invalidStudio).Error)
			invalidStudio = studio
			invalidStudio.ID = 8
			invalidStudio.BindingID = 999
			require.Error(t, db.Table(deploymentPrefix+"templates_studio_records").Create(&invalidStudio).Error)
			invalidStudio = studio
			invalidStudio.ID = 9
			invalidStudio.State = "VERIFIED"
			require.Error(t, db.Table(deploymentPrefix+"templates_studio_records").Create(&invalidStudio).Error)

		})
	}
}

func verifyTemplatesEvaluationRollback(t *testing.T, raw *sql.DB, dialect string) {
	t.Helper()
	source := (&templates.Module{}).MigrationSource()
	source.Prefix = "evalguard_"
	require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, dialect, 7))
	db := openMilestoneGORM(t, raw, dialect)
	at := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	version := model.Version{ID: 1, TenantID: 1, Key: "rollback", Semver: "1.0.0", State: "PUBLISHED", Definition: "{}", SHA256: "hash", Version: 1, CreatedBy: 11, UpdatedBy: 11, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, db.Table(source.Prefix+"templates_versions").Create(&version).Error)
	rows := []struct {
		table string
		value any
	}{
		{"templates_eval_cases", &model.EvalCase{ID: 2, TenantID: 1, TemplateVersionID: 1, TemplateKey: "rollback", Origin: "MANUAL", Kind: "JUDGEMENT", Title: "Keep case", InputRef: "{}", Expected: "Expected", SourceSnapshot: "{}", Version: 1, CreatedBy: 11, CreatedAt: at}},
		{"templates_eval_runs", &model.EvalRun{ID: 3, TenantID: 1, TemplateVersionID: 1, TemplateKey: "rollback", TargetKey: "target", Snapshot: "{}", SHA256: "hash", State: "COMPLETED", Results: "[]", CreatedBy: 11, StartedAt: at}},
	}
	for _, row := range rows {
		require.NoError(t, db.Table(source.Prefix+row.table).Create(row.value).Error)
		for range 2 {
			err := goosex.MigrateDownTo(t.Context(), raw, source, dialect, 6)
			require.Error(t, err, "populated evaluation history must survive rollback")
			require.NotContains(t, err.Error(), "already exists", "retry must reach the data guard")
			require.NotContains(t, err.Error(), "does not support temporary tables")
			require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+row.table))
		}
		require.NoError(t, db.Table(source.Prefix+row.table).Where("tenant_id = ?", 1).Delete(row.value).Error)
	}
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, dialect, 6))
	assertTableExists(t, raw, dialect, source.Prefix+"templates_eval_cases", false)
	assertTableExists(t, raw, dialect, source.Prefix+"templates_eval_runs", false)
	assertTableExists(t, raw, dialect, source.Prefix+"eval_rollback_guard", false)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, dialect, 0))
}
