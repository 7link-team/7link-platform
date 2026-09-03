package migration_test

import (
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/identity/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMIdentityModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{model.Principal{}, model.ServiceAccount{}, model.ServiceAccountCredential{}}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	require.Len(t, models, 3)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			require.NoError(t, goosex.Migrate(t.Context(), db, (&identity.Module{}).MigrationSource(), engine.dialect))
			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) {
					assertModelMatchesTable(t, db, engine.dialect, persisted)
				})
			}
		})
	}
}
