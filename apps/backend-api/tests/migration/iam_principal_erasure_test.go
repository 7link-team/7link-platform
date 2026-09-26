package migration_test

import (
	"database/sql"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/federation"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The erasure rollback contract predates the irreversible binding-trust upgrade.
type beforeBindingTrust struct{ module.Module }

func (m beforeBindingTrust) MigrationSource() goosex.Source {
	source := m.Module.(module.MigrationSourceProvider).MigrationSource()
	files := fstest.MapFS{}
	err := fs.WalkDir(source.FS, "sql", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.HasSuffix(path, "_binding_trust.sql") {
			data, err := fs.ReadFile(source.FS, path)
			if err != nil {
				return err
			}
			files[path] = &fstest.MapFile{Data: data}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	source.FS = files
	return source
}

func (m beforeBindingTrust) MigrationMilestones() []module.MigrationMilestone {
	all := m.Module.(module.MigrationMilestoneProvider).MigrationMilestones()
	return all[:len(all)-1]
}

func beforeBindingTrustModules() []module.Module {
	modules := realMySQLMigrationModules()
	for i, owner := range modules {
		if owner.ModuleName() == federation.Name {
			modules[i] = beforeBindingTrust{owner}
		}
	}
	return modules
}

func TestIAMPrincipalErasureOwnerMigrations(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gdb := openMilestoneGORM(t, db, engine.dialect)
			const prefix = "erase_"
			modules := beforeBindingTrustModules()
			_, err := app.ExecuteModuleMigrations(t.Context(), gdb, modules, app.MigrationRequest{Action: app.MigrationUp}, prefix)
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Millisecond)
			principal := identitymodel.Principal{ID: 1, LoginName: "erase-owner", DisplayName: "Before", Status: "disabled", CreatedAt: now, UpdatedAt: now}
			require.NoError(t, gdb.Table(prefix+"iam_principals").Create(&principal).Error)
			row := authnmodel.NotificationOutbox{ID: 1, PrincipalID: &principal.ID, Kind: "email", Recipient: "before@example.test",
				PayloadCiphertext: "sealed", Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, gdb.Table(prefix+"iam_notification_outbox").Create(&row).Error)
			lifecycle := identitymodel.PrincipalLifecycle{PrincipalID: 1, Version: 1, RetainUntil: now.Add(-time.Hour), Reference: "case", UpdatedAt: now}
			require.NoError(t, gdb.Table(prefix+"iam_principal_lifecycles").Create(&lifecycle).Error)
			var staleWriter *gorm.DB
			// The in-process MySQL compatibility driver has no isolation-level support.
			if engine.name == "postgres" || engine.name == "mysql8" {
				staleWriter = gdb.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				require.NoError(t, staleWriter.Error)
				t.Cleanup(func() { _ = staleWriter.Rollback().Error })
				var snapshot identitymodel.PrincipalLifecycle
				require.NoError(t, staleWriter.Table(prefix+"iam_principal_lifecycles").Where("principal_id = 1").Take(&snapshot).Error)
				require.Nil(t, snapshot.AnonymizedAt)
			}
			require.NoError(t, gdb.Transaction(func(tx *gorm.DB) error {
				if engine.dialect != "sqlite" {
					var locked identitymodel.Principal
					if err := tx.Table(prefix + "iam_principals").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = 1").Take(&locked).Error; err != nil {
						return err
					}
				}
				if err := authn.ErasePrincipalData(t.Context(), tx, prefix, principal.ID, now); err != nil {
					return err
				}
				if err := tx.Table(prefix + "iam_principals").Where("id = 1").Updates(map[string]any{
					"login_name": "erased:1", "display_name": "Anonymized account", "email": "",
				}).Error; err != nil {
					return err
				}
				return tx.Table(prefix+"iam_principal_lifecycles").Where("principal_id = 1").Update("anonymized_at", now).Error
			}))
			if staleWriter != nil {
				// A writer with a pre-erasure snapshot must not restore PII. PostgreSQL
				// may reject with serialization failure; MySQL must observe the tombstone.
				assert.Error(t, staleWriter.Table(prefix+"iam_notification_outbox").Create(&row).Error)
				require.NoError(t, staleWriter.Rollback().Error)
			}
			assert.ErrorContains(t, gdb.Table(prefix+"iam_notification_outbox").Create(&row).Error, "anonymized")
			for _, owner := range modules {
				provider := owner.(module.MigrationMilestoneProvider)
				for _, milestone := range provider.MigrationMilestones() {
					if milestone.Name != "principal-erasure" {
						continue
					}
					source := owner.(module.MigrationSourceProvider).MigrationSource()
					source.Prefix = prefix
					assert.ErrorContains(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, milestone.Version-1), "anonymized", owner.ModuleName())
				}
			}
			assert.ErrorContains(t, gdb.Table(prefix+"iam_notification_outbox").Create(&row).Error, "anonymized")
		})
	}
}
