package migration_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/identity/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIAMPrincipalLifecycleMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				if engine.name == "mysql8" {
					prefix = "lifecycle_" + prefix
				}
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					gdb := openMilestoneGORM(t, db, engine.dialect)
					source := (&identity.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 2))
					now := time.Now().UTC().Truncate(time.Millisecond)
					principal := model.Principal{ID: 1, LoginName: "before-upgrade", DisplayName: "Before", Email: "before@example.test",
						Status: "disabled", CreatedAt: now, UpdatedAt: now, DisabledAt: &now}
					require.NoError(t, gdb.Table(prefix+"iam_principals").Omit("Avatar", "SelfRegistered").Create(&principal).Error)
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					var loaded model.Principal
					require.NoError(t, gdb.Table(prefix+"iam_principals").Where("id = 1").Take(&loaded).Error)
					assert.Equal(t, principal.Email, loaded.Email)
					assert.False(t, loaded.SelfRegistered, "an existing principal is never taken for a self-registration")
					// Clean installations retain structural rollback before any erasure.
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 2))
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					lifecycle := model.PrincipalLifecycle{PrincipalID: 1, Version: 1, RetainUntil: now.Add(-time.Hour),
						Reference: "case", UpdatedAt: now}
					require.NoError(t, gdb.Table(prefix+"iam_principal_lifecycles").Create(&lifecycle).Error)
					require.NoError(t, gdb.Table(prefix+"iam_principals").Where("id = 1").Updates(map[string]any{
						"login_name": "erased:1", "display_name": "Anonymized account", "email": "",
					}).Error)
					require.NoError(t, gdb.Table(prefix+"iam_principal_lifecycles").Where("principal_id = 1").Update("anonymized_at", now).Error)
					for _, column := range []string{"email", "display_name", "login_name", "status"} {
						value := "restored"
						if column == "status" {
							value = "active"
						}
						assert.Error(t, gdb.Table(prefix+"iam_principals").Where("id = 1").Update(column, value).Error, column)
					}
					assert.Error(t, gdb.Table(prefix+"iam_principals").Where("id = 1").Delete(map[string]any{}).Error)
					assert.Error(t, gdb.Table(prefix+"iam_principal_lifecycles").Where("principal_id = 1").Update("anonymized_at", nil).Error)
					assert.Error(t, gdb.Table(prefix+"iam_principal_lifecycles").Where("principal_id = 1").Delete(map[string]any{}).Error)
					err := goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 2)
					require.ErrorContains(t, err, "permanently anonymized")
					assert.Error(t, gdb.Table(prefix+"iam_principals").Where("id = 1").Update("status", "active").Error,
						"failed downgrade must leave the guard installed")
				})
			}
		})
	}
}
