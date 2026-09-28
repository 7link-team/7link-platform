package migration_test

import (
	"testing"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMAuditModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{model.Event{}, model.Head{}, model.RetentionPolicy{}}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	require.Len(t, models, 3)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gormDB := openMilestoneGORM(t, db, engine.dialect)
			_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, []module.Module{&iamaudit.Module{}}, app.MigrationRequest{Action: app.MigrationUp}, "")
			require.NoError(t, err)
			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) { assertModelMatchesTable(t, db, engine.dialect, persisted) })
			}
		})
	}
}
