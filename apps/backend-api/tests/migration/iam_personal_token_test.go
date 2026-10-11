package migration_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/federation"
	federationmodel "7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/identity"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real engines verify digest uniqueness, immutable ownership, erasure fences,
// far-future storage and an additive upgrade over a populated v13 installation.
func TestIAMPersonalTokenMigrations(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
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
			for _, prefix := range []string{"personal_", "sl_personal_"} {
				t.Run(prefix, func(t *testing.T) {
					db := engine.open(t)
					gdb := openMilestoneGORM(t, db, engine.dialect)
					identitySource := (&identity.Module{}).MigrationSource()
					identitySource.Prefix = prefix
					organizationSource := (&organization.Module{}).MigrationSource()
					organizationSource.Prefix = prefix
					source := (&authn.Module{}).MigrationSource()
					source.Prefix = prefix
					federationSource := (&federation.Module{}).MigrationSource()
					federationSource.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, identitySource, engine.dialect, 4))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, organizationSource, engine.dialect, 7))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 13))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, federationSource, engine.dialect, 5))
					now := time.Date(2026, 9, 10, 0, 0, 0, 123000000, time.UTC)
					principals := []identitymodel.Principal{
						{ID: 101, LoginName: "legacy-owner", DisplayName: "Legacy owner", Status: "active", CreatedAt: now, UpdatedAt: now},
						{ID: 102, LoginName: "other-owner", DisplayName: "Other owner", Status: "active", CreatedAt: now, UpdatedAt: now},
					}
					require.NoError(t, gdb.Table(prefix+"iam_principals").Omit("Avatar", "SelfRegistered", "Phone", "PhoneVerified").Create(&principals).Error)
					require.NoError(t, gdb.Table(prefix+"iam_tenants").Create(map[string]any{"id": 71, "slug": "personal", "name": "Personal", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
					credential := authnmodel.Credential{PrincipalID: 101, PasswordHash: "legacy-hash", CredentialVersion: 3, PasswordChangedAt: now, UpdatedAt: now}
					require.NoError(t, gdb.Table(prefix+"iam_credentials").Create(&credential).Error)
					require.NoError(t, insertFederationProvider(t, db, prefix, 1, 71, "Existing IdP", "https://idp.example.test", "client", "oidc", "active"))
					require.NoError(t, insertFederationLink(t, db, prefix, 1, 71, 101, "legacy-subject"))
					for cycle := range 2 {
						require.NoError(t, goosex.MigrateUpTo(t.Context(), db, identitySource, engine.dialect, 6))
						require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 15))
						require.NoError(t, goosex.MigrateUpTo(t.Context(), db, federationSource, engine.dialect, 6))
						assert.Equal(t, 1, tableRowCount(t, db, prefix+"iam_identity_links"))
						var loaded authnmodel.Credential
						require.NoError(t, gdb.Table(prefix+"iam_credentials").Where("principal_id = ?", 101).Take(&loaded).Error)
						assert.Equal(t, "legacy-hash", loaded.PasswordHash)
						assert.EqualValues(t, 3, loaded.CredentialVersion)
						if cycle == 0 {
							require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 13))
							require.NoError(t, goosex.MigrateDownTo(t.Context(), db, federationSource, engine.dialect, 5))
							require.NoError(t, goosex.MigrateDownTo(t.Context(), db, identitySource, engine.dialect, 4))
						}
					}
					future := time.Date(9999, 12, 31, 23, 59, 59, 999000000, time.UTC)
					row := authnmodel.PersonalToken{ID: 201, TokenHash: "digest", PrincipalID: 101, TenantID: 71, Name: "个人 😀 10%", Permissions: `["store_view"]`, CredentialVersion: 3, CreatedAt: now, ExpiresAt: future}
					require.NoError(t, gdb.Table(prefix+row.TableName()).Create(&row).Error)
					var saved authnmodel.PersonalToken
					require.NoError(t, gdb.Table(prefix+row.TableName()).Where("id = ?", row.ID).Take(&saved).Error)
					assert.True(t, future.Equal(saved.ExpiresAt))
					assert.Equal(t, row.Name, saved.Name)
					duplicate := row
					duplicate.ID = 202
					assert.Error(t, gdb.Table(prefix+row.TableName()).Create(&duplicate).Error)
					assert.ErrorContains(t, gdb.Table(prefix+row.TableName()).Where("id = ?", row.ID).Update("principal_id", 102).Error, "immutable")
					reauth := authnmodel.ReauthenticationChallenge{IDHash: "proof-digest", PrincipalID: 101, SessionHash: "session", CredentialVersion: 3, Purpose: "personal_token_create", Target: "fingerprint", Method: "password", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
					require.NoError(t, gdb.Table(prefix+reauth.TableName()).Create(&reauth).Error)
					assert.ErrorContains(t, gdb.Table(prefix+reauth.TableName()).Where("id_hash = ?", reauth.IDHash).Update("principal_id", 102).Error, "immutable")
					linkTx := federationmodel.IdentityLinkTransaction{IDHash: "link-tx", PrincipalID: 101, ProviderID: 1, TenantID: 71, SessionHash: "session", Purpose: "link", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
					assertModelMatchesNamedTable(t, db, engine.dialect, linkTx, prefix+linkTx.TableName())
					require.NoError(t, gdb.Table(prefix+linkTx.TableName()).Create(&linkTx).Error)
					assert.ErrorContains(t, gdb.Table(prefix+linkTx.TableName()).Where("id_hash = ?", linkTx.IDHash).Update("principal_id", 102).Error, "immutable")
					closure := identitymodel.ClosureRequest{PrincipalID: 101, Status: "pending", Version: 1, RequestedAt: now, UpdatedAt: now}
					assertModelMatchesNamedTable(t, db, engine.dialect, closure, prefix+closure.TableName())
					require.NoError(t, gdb.Table(prefix+closure.TableName()).Create(&closure).Error)
					assert.ErrorContains(t, gdb.Table(prefix+closure.TableName()).Where("principal_id = ?", 101).Update("principal_id", 102).Error, "immutable")
					assert.Error(t, gdb.Table(prefix+closure.TableName()).Where("principal_id = ?", 101).Update("status", "completed").Error, "completion is derived, never directly writable")
					assert.Error(t, gdb.Table(prefix+closure.TableName()).Where("principal_id = ?", 101).Update("version", 0).Error)
					lifecycle := identitymodel.PrincipalLifecycle{PrincipalID: 101, Version: 1, RetainUntil: now, Reference: "test-erasure", UpdatedAt: now}
					require.NoError(t, gdb.Table(prefix+lifecycle.TableName()).Create(&lifecycle).Error)
					require.NoError(t, gdb.Table(prefix+"iam_principals").Where("id = ?", 101).Updates(map[string]any{"status": "disabled", "disabled_at": now}).Error)
					require.NoError(t, gdb.Table(prefix+lifecycle.TableName()).Where("principal_id = ?", 101).Update("anonymized_at", now).Error)
					closureGuard := "anonymized"
					if engine.dialect == "sqlite" {
						closureGuard = "tombstones cannot be changed"
					}
					assert.ErrorContains(t, gdb.Table(prefix+closure.TableName()).Where("principal_id = ?", 101).Update("status", "processing").Error, closureGuard)
					assert.ErrorContains(t, gdb.Table(prefix+closure.TableName()).Create(&closure).Error, closureGuard)
					var retainedClosure identitymodel.ClosureRequest
					require.NoError(t, gdb.Table(prefix+closure.TableName()).Where("principal_id = ?", 101).Take(&retainedClosure).Error)
					assert.Equal(t, "pending", retainedClosure.Status)
					assert.EqualValues(t, 1, retainedClosure.Version)
					assert.ErrorContains(t, gdb.Table(prefix+row.TableName()).Where("id = ?", row.ID).Update("name", "restored").Error, "anonymized")
					duplicate.TokenHash = "new-digest"
					assert.ErrorContains(t, gdb.Table(prefix+row.TableName()).Create(&duplicate).Error, "anonymized")
					assert.ErrorContains(t, gdb.Table(prefix+reauth.TableName()).Where("id_hash = ?", reauth.IDHash).Update("target", "restored").Error, "anonymized")
					reauth.IDHash = "new-proof"
					assert.ErrorContains(t, gdb.Table(prefix+reauth.TableName()).Create(&reauth).Error, "anonymized")
					assert.ErrorContains(t, gdb.Table(prefix+linkTx.TableName()).Where("id_hash = ?", linkTx.IDHash).Update("email", "restored@example.test").Error, "anonymized")
					linkTx.IDHash = "new-link-tx"
					assert.ErrorContains(t, gdb.Table(prefix+linkTx.TableName()).Create(&linkTx).Error, "anonymized")
					require.NoError(t, federation.ErasePrincipalData(t.Context(), gdb, prefix, 101, now))
					assert.Zero(t, tableRowCount(t, db, prefix+linkTx.TableName()))
					assert.Zero(t, tableRowCount(t, db, prefix+"iam_identity_links"))
				})
			}
		})
	}
}
