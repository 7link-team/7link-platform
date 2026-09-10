package migration_test

import (
	"database/sql"
	"fmt"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthGrantMigrationPreservesLegacyRevocation(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			for _, prefix := range []string{"grant_", "sl_grant_"} {
				t.Run(prefix, func(t *testing.T) {
					verifyOAuthGrantMigration(t, db, engine.dialect, prefix)
				})
			}
		})
	}
}

// This also runs on real MySQL: additive grant metadata must not manufacture
// authorization for old rows or erase pre-existing token revocation on rollback.
func verifyOAuthGrantMigration(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	identitySource := (&identity.Module{}).MigrationSource()
	identitySource.Prefix = prefix
	organizationSource := (&organization.Module{}).MigrationSource()
	organizationSource.Prefix = prefix
	oauthSource := (&oauth.Module{}).MigrationSource()
	oauthSource.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), db, identitySource, dialect))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, organizationSource, dialect, 7))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, oauthSource, dialect, 9))
	const now = "2026-09-03 18:00:00.123"
	for _, statement := range []string{
		fmt.Sprintf("INSERT INTO %siam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (101, 'grant-user', 'Grant user', 'active', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (7, 'grant-tenant', 'Grant tenant', 'active', 1, '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_clients (id, tenant_id, name, created_at, updated_at) VALUES (22, 7, 'Legacy app', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_consents (principal_id, client_id, tenant_id, scope, created_at, last_used_at) VALUES (101, 22, 7, 'openid', '%s', '%s')", prefix, now, now),
		fmt.Sprintf("INSERT INTO %siam_oauth_codes (code_hash, client_id, principal_id, redirect_uri, scope, code_challenge, auth_time, created_at, expires_at, consumed_at) VALUES ('legacy-code', 22, 101, 'https://client.example/callback', 'openid', 'challenge', '%s', '%s', '%s', '%s')", prefix, now, now, now, now),
		fmt.Sprintf("INSERT INTO %siam_refresh_tokens (id_hash, family_id, principal_id, client_id, scope, auth_time, created_at, expires_at, used_at, revoked_at) VALUES ('legacy-refresh', 'legacy-family', 101, 22, 'openid', '%s', '%s', '%s', '%s', '%s')", prefix, now, now, now, now, now),
	} {
		_, err := db.ExecContext(t.Context(), statement)
		require.NoError(t, err)
	}
	tables := []string{"iam_oauth_consents", "iam_oauth_codes", "iam_refresh_tokens"}
	assertPreserved := func() {
		t.Helper()
		for _, table := range tables {
			var scope string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT scope FROM "+prefix+table).Scan(&scope))
			assert.Equal(t, "openid", scope)
			assert.Equal(t, 1, tableRowCount(t, db, prefix+table))
		}
		var consumed, revoked, used bool
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT consumed_at IS NOT NULL FROM "+prefix+"iam_oauth_codes").Scan(&consumed))
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT used_at IS NOT NULL, revoked_at IS NOT NULL FROM "+prefix+"iam_refresh_tokens").Scan(&used, &revoked))
		assert.True(t, consumed)
		assert.True(t, used)
		assert.True(t, revoked)
	}
	for cycle := range 2 {
		require.NoError(t, goosex.MigrateUpTo(t.Context(), db, oauthSource, dialect, 10))
		assertPreserved()
		for _, table := range tables {
			var grant string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT grant_id FROM "+prefix+table).Scan(&grant))
			assert.Empty(t, grant, "schema upgrade never creates an authorization incarnation")
			_, err := db.ExecContext(t.Context(), "UPDATE "+prefix+table+" SET grant_id = 'new-incarnation'")
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT grant_id FROM "+prefix+table).Scan(&grant))
			assert.Equal(t, "new-incarnation", grant)
		}
		if cycle == 0 {
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, oauthSource, dialect, 9))
			assertPreserved()
		}
	}
	// Test-owned schemas only; leave the shared real-MySQL matrix pristine.
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, oauthSource, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, organizationSource, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, identitySource, dialect, 0))
}
