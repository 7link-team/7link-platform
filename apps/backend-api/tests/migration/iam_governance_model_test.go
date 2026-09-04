package migration_test

import (
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/governance/model"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMGovernanceModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{model.AccessRequest{}, model.ApprovalStep{}, model.AccessReview{}, model.AccessReviewItem{}}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	require.Len(t, models, 4)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gormDB := openMilestoneGORM(t, db, engine.dialect)
			_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, governanceMigrationModules(), app.MigrationRequest{Action: app.MigrationUp}, "")
			require.NoError(t, err)
			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) { assertModelMatchesTable(t, db, engine.dialect, persisted) })
			}
		})
	}
}
