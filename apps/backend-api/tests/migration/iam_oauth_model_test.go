package migration_test

import (
	"database/sql"
	"fmt"
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/identity"
	iamoauth "7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIAMOAuthModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{model.Client{}, model.AuthorizationCode{}, model.Consent{}, model.RefreshToken{}}
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
			_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, organizationMigrationModules(), app.MigrationRequest{
				Action: app.MigrationUp,
			}, "")
			require.NoError(t, err)
			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) {
					assertModelMatchesTable(t, db, engine.dialect, persisted)
				})
			}
		})
	}
}

func TestIAMOAuthSQLiteConstraintUpgradePreservesRows(t *testing.T) {
	db := testdb.OpenSQLite(t)
	prefix := deploymentPrefix
	identitySource := (&identity.Module{}).MigrationSource()
	identitySource.Prefix = prefix
	organizationSource := (&organization.Module{}).MigrationSource()
	organizationSource.Prefix = prefix
	oauthSource := (&iamoauth.Module{}).MigrationSource()
	oauthSource.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), db, identitySource, "sqlite"))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, organizationSource, "sqlite", 7))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, oauthSource, "sqlite", 4))

	const now = "2026-09-03 18:00:00.123"
	statements := []string{
		fmt.Sprintf("INSERT INTO %siam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (101, 'alice', 'Alice', 'active', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (7, 'tenant', 'Tenant', 'active', 1, '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_clients (id, tenant_id, name, created_at, updated_at) VALUES (22, 7, 'Browser', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_codes (code_hash, client_id, principal_id, redirect_uri, scope, code_challenge, auth_time, created_at, expires_at) VALUES ('code', 22, 101, 'https://app.example/callback', 'openid', 'challenge', '%s', '%s', '%s')", prefix, now, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_consents (principal_id, client_id, tenant_id, scope, created_at, last_used_at) VALUES (101, 22, 7, 'openid', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_refresh_tokens (id_hash, family_id, principal_id, client_id, scope, auth_time, created_at, expires_at) VALUES ('refresh', 'family', 101, 22, 'openid', '%s', '%s', '%s')", prefix, now, now, now),
	}
	for _, statement := range statements {
		_, err := db.ExecContext(t.Context(), statement)
		require.NoError(t, err)
	}
	require.NoError(t, goosex.Migrate(t.Context(), db, oauthSource, "sqlite"))
	for _, table := range []string{"iam_oauth_consents", "iam_oauth_codes", "iam_refresh_tokens"} {
		var grant string
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT grant_id FROM "+prefix+table+" LIMIT 1").Scan(&grant))
		assert.Empty(t, grant, "legacy rows must not receive a usable grant during migration")
	}

	var redirectURI, codeScope, challenge string
	var consumedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT redirect_uri, scope, code_challenge, consumed_at FROM %siam_oauth_codes WHERE code_hash = 'code'", prefix,
	)).Scan(&redirectURI, &codeScope, &challenge, &consumedAt))
	assert.Equal(t, "https://app.example/callback", redirectURI)
	assert.Equal(t, "openid", codeScope)
	assert.Equal(t, "challenge", challenge)
	assert.False(t, consumedAt.Valid)

	var consentTenant int64
	var consentScope string
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT tenant_id, scope FROM %siam_oauth_consents WHERE principal_id = 101 AND client_id = 22", prefix,
	)).Scan(&consentTenant, &consentScope))
	assert.Equal(t, int64(7), consentTenant)
	assert.Equal(t, "openid", consentScope)

	var familyID, refreshScope string
	var usedAt, revokedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT family_id, scope, used_at, revoked_at FROM %siam_refresh_tokens WHERE id_hash = 'refresh'", prefix,
	)).Scan(&familyID, &refreshScope, &usedAt, &revokedAt))
	assert.Equal(t, "family", familyID)
	assert.Equal(t, "openid", refreshScope)
	assert.False(t, usedAt.Valid)
	assert.False(t, revokedAt.Valid)
}
