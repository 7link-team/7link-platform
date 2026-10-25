package migration_test

import (
	"7link.com/platform/internal/performance"
	"7link.com/platform/internal/performance/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPerformanceSchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&performance.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Scheme{}, deploymentPrefix+"performance_schemes")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Revision{}, deploymentPrefix+"performance_scheme_revisions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Report{}, deploymentPrefix+"performance_reports")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReportRevision{}, deploymentPrefix+"performance_report_revisions")
			if engine.name == "mysql" {
				return
			} // schema harness does not enforce production constraints.
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			require.NoError(t, db.Table(deploymentPrefix+"performance_schemes").Create(&model.Scheme{ID: 1, TenantID: 1, Version: 1, Enabled: true, CreatedAt: at, UpdatedAt: at}).Error)
			r := model.Revision{ID: 2, TenantID: 1, SchemeID: 1, Version: 1, Definition: "{}", SHA256: "hash", Reason: "initial", CreatedBy: 11, CreatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"performance_scheme_revisions").Create(&r).Error)
			r.ID = 3
			require.Error(t, db.Table(deploymentPrefix+"performance_scheme_revisions").Create(&r).Error, "logical revision cannot duplicate")
			r.Version = 2
			r.TenantID = 2
			require.Error(t, db.Table(deploymentPrefix+"performance_scheme_revisions").Create(&r).Error, "revision cannot attach across tenants")
			report := model.Report{ID: 20, TenantID: 1, SchemeID: 1, SubjectKind: "HUMAN", SubjectID: 11, Cadence: "MONTH", PeriodStart: "2040-01-01", PeriodEnd: "2040-01-31", Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"performance_reports").Create(&report).Error)
			report.ID = 21
			require.Error(t, db.Table(deploymentPrefix+"performance_reports").Create(&report).Error, "one logical subject/scheme/period report")
			report.TenantID = 2
			require.Error(t, db.Table(deploymentPrefix+"performance_reports").Create(&report).Error, "report cannot attach across tenants")
			revision := model.ReportRevision{ID: 30, TenantID: 1, ReportID: 20, Version: 1, Snapshot: "{}", SHA256: "hash", Reason: "generation", CreatedBy: 11, CreatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"performance_report_revisions").Create(&revision).Error)
			revision.ID = 31
			require.Error(t, db.Table(deploymentPrefix+"performance_report_revisions").Create(&revision).Error, "frozen revision identity is unique")
			revision.Version = 2
			revision.TenantID = 2
			require.Error(t, db.Table(deploymentPrefix+"performance_report_revisions").Create(&revision).Error, "revision cannot attach across tenants")

		})
	}
}
