package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/identity"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const federationTestTime = "2026-09-02 00:00:00"

func TestIAMFederationMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					verifyFederationConstraintsNeedIdentity(t, db, engine.dialect, prefix+"missing_")
					verifyFederationConstraintRoundTrip(t, db, engine.dialect, prefix+"data_")

					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := federationMigrationModules()
					assertFederationUp(t, gormDB, modules, prefix)
					verifyFederationContracts(t, db, prefix)
					verifyFederationRollbackGraph(t, db, gormDB, modules, engine.dialect, prefix)
				})
			}
		})
	}
}

func federationMigrationModules() []module.Module {
	// Deliberately not dependency ordered: the milestone graph owns ordering.
	return []module.Module{&federation.Module{}, &identity.Module{}}
}

func verifyFederationConstraintsNeedIdentity(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&federation.Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 2))
	err := goosex.MigrateUpTo(t.Context(), db, source, dialect, 3)
	assert.Error(t, err, "federation constraints must not run before identity base")
	status, inspectErr := goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, inspectErr)
	assert.Equal(t, int64(2), status.Current)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 0))
}

func verifyFederationConstraintRoundTrip(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	identitySource := (&identity.Module{}).MigrationSource()
	identitySource.Prefix = prefix
	federationSource := (&federation.Module{}).MigrationSource()
	federationSource.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), db, identitySource, dialect))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, federationSource, dialect, 2))

	seedFederationPrincipal(t, db, prefix, 1, "roundtrip")
	require.NoError(t, insertFederationProvider(t, db, prefix, 1, 1, "Round Trip", "https://roundtrip.example", "client", "oidc", "active"))
	require.NoError(t, insertFederationLink(t, db, prefix, 1, 1, 1, "roundtrip"))

	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, federationSource, dialect, 3))
	assert.Equal(t, 1, tableRowCount(t, db, prefix+"iam_identity_links"))
	assert.Error(t, insertFederationLink(t, db, prefix, 1, 1, 999, "orphan"))

	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, federationSource, dialect, 2))
	assert.Equal(t, 1, tableRowCount(t, db, prefix+"iam_identity_links"))
	require.NoError(t, insertFederationLink(t, db, prefix, 1, 1, 999, "temporary-orphan"))
	require.NoError(t, execFederationSQL(t, db,
		"DELETE FROM %siam_identity_links WHERE provider_id = 1 AND external_subject = 'temporary-orphan'", prefix))

	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, federationSource, dialect, 3))
	assert.Equal(t, 1, tableRowCount(t, db, prefix+"iam_identity_links"))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, federationSource, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, identitySource, dialect, 0))
}

func assertFederationUp(t *testing.T, db *gorm.DB, modules []module.Module, prefix string) {
	t.Helper()
	reports, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationUp,
	}, prefix)
	require.NoError(t, err)
	require.Len(t, reports, len(modules))
	milestones := make(map[string]string, len(reports))
	for _, report := range reports {
		milestones[report.Module] = report.Milestone
		assert.Equal(t, goosex.StateClean, report.Status.State)
	}
	assert.Equal(t, "self-registration-origin", milestones[identity.Name])
	assert.Equal(t, "binding-trust", milestones[federation.Name])
}

func TestLegacyFederationBindingUpgradePreservesUnknownOwnership(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					identitySource := (&identity.Module{}).MigrationSource()
					identitySource.Prefix = prefix
					source := (&federation.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, identitySource, engine.dialect))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 6))
					seedFederationPrincipal(t, db, prefix, 991, "legacy-binding")
					require.NoError(t, insertFederationProvider(t, db, prefix, 991, 1, "Legacy", "https://legacy.example", "client", "oidc", "active"))
					require.NoError(t, insertFederationLink(t, db, prefix, 991, 1, 991, "legacy-subject"))
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					var provenance, fingerprint, evidence, subject string
					var principalID int64
					var quarantined sql.NullTime
					require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT binding_provenance, security_fingerprint, recovery_evidence, external_subject, principal_id, quarantined_at FROM %siam_identity_links WHERE provider_id = 991", prefix)).Scan(&provenance, &fingerprint, &evidence, &subject, &principalID, &quarantined))
					assert.Equal(t, federation.BindingUnknown, provenance)
					assert.Empty(t, fingerprint)
					assert.Empty(t, evidence)
					assert.Equal(t, "legacy-subject", subject)
					assert.Equal(t, int64(991), principalID)
					assert.False(t, quarantined.Valid)
					require.NoError(t, execFederationSQL(t, db, "UPDATE %siam_identity_links SET recovery_evidence = 'independent case 42' WHERE provider_id = 991", prefix))
					assert.ErrorContains(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 6), "binding trust downgrade is forbidden")
					require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT recovery_evidence FROM %siam_identity_links WHERE provider_id = 991", prefix)).Scan(&evidence))
					assert.Equal(t, "independent case 42", evidence, "rollback retains ownership evidence")
				})
			}
		})
	}
}

func verifyFederationContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	claims := prefix + "iam_federation_replay_claims"
	for _, key := range []string{"id-A", "id-a"} {
		query := fmt.Sprintf("INSERT INTO %s (claim_key, expires_at, created_at) VALUES ('%s', '%s', '%s')", claims, key, federationTestTime, federationTestTime)
		_, err := db.ExecContext(t.Context(), query)
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), query)
		require.Error(t, err, "replayed protocol identifiers must violate the primary key")
	}
	for _, key := range []string{"", strings.Repeat("a", 256)} {
		_, err := db.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO %s (claim_key, expires_at, created_at) VALUES ('%s', '%s', '%s')", claims, key, federationTestTime, federationTestTime))
		require.Error(t, err, "invalid claim keys must be rejected by the schema")
	}
	for id, login := range map[int]string{100: "subject-upper", 101: "subject-lower", 102: "subject-long"} {
		seedFederationPrincipal(t, db, prefix, id, login)
	}

	providers := prefix + "iam_identity_providers"
	require.NoError(t, insertFederationProvider(t, db, prefix, 1, 1, "Primary", "https://IDP.example", "client", "oidc", "active"))
	require.NoError(t, insertFederationProvider(t, db, prefix, 2, 1, "Case Variant", "https://idp.example", "client", "oidc", "active"),
		"OIDC issuers use binary comparison")
	assert.Error(t, insertFederationProvider(t, db, prefix, 3, 1, "Duplicate", "https://IDP.example", "client", "oidc", "active"))
	assert.Error(t, insertFederationProvider(t, db, prefix, 4, 1, "Bad Type", "https://type.example", "client", "OIDC", "active"))
	assert.Error(t, insertFederationProvider(t, db, prefix, 5, 1, "Bad Status", "https://status.example", "client", "oidc", "ACTIVE"))
	assert.Error(t, execFederationSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, issuer, client_id, auto_provision, created_at, updated_at) VALUES (6, 1, 'Bad Boolean', 'https://boolean.example', 'client', 2, '%s', '%s')",
		providers, federationTestTime, federationTestTime))
	assert.Error(t, insertFederationProvider(t, db, prefix, 7, 1, strings.Repeat("界", 43), "https://name.example", "client", "oidc", "active"),
		"provider names retain the source 128-byte limit")
	assertFederationProviderDefaults(t, db, providers)

	links := prefix + "iam_identity_links"
	assert.Error(t, insertFederationLink(t, db, prefix, 1, 2, 100, "wrong-tenant"),
		"provider links must use the provider tenant")
	assert.Error(t, insertFederationLink(t, db, prefix, 1, 1, 999, "orphan-principal"),
		"identity links require an existing principal")
	assert.Error(t, execFederationSQL(t, db,
		"INSERT INTO %s (provider_id, tenant_id, principal_id, external_subject, created_at, updated_at) VALUES (1, 1, 102, 'missing-login-time', '%s', '%s')",
		links, federationTestTime, federationTestTime), "identity links require a real last-login timestamp")
	require.NoError(t, insertFederationLink(t, db, prefix, 1, 1, 100, "Subject"))
	require.NoError(t, insertFederationLink(t, db, prefix, 1, 1, 101, "subject"),
		"external subjects use binary comparison")
	assert.Error(t, insertFederationLink(t, db, prefix, 1, 1, 102, strings.Repeat("x", 129)))
	require.NoError(t, execFederationSQL(t, db, "DELETE FROM %siam_principals WHERE id = 100", prefix))
	assert.Equal(t, 1, tableRowCount(t, db, links), "principal deletion cascades only that principal's link")
	require.NoError(t, execFederationSQL(t, db, "DELETE FROM %siam_identity_providers WHERE id = 1", prefix))
	assert.Zero(t, tableRowCount(t, db, links), "provider deletion cascades remaining links")

	verifyFederationSAMLContracts(t, db, prefix)
}

func verifyFederationSAMLContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	serviceProviders := prefix + "iam_saml_service_providers"
	require.NoError(t, insertFederationSAMLSP(t, db, serviceProviders, 20, "https://SP.example", "<xml/>", "active"))
	require.NoError(t, insertFederationSAMLSP(t, db, serviceProviders, 21, "https://sp.example", "<xml/>", "active"),
		"SAML entity IDs use binary comparison")
	assert.Error(t, insertFederationSAMLSP(t, db, serviceProviders, 22, "https://SP.example", "<xml/>", "active"))
	assert.Error(t, insertFederationSAMLSP(t, db, serviceProviders, 23, "https://status.example", "<xml/>", "ACTIVE"))
	assert.Error(t, insertFederationSAMLSP(t, db, serviceProviders, 24, "https://large.example", strings.Repeat("x", 65537), "active"),
		"SAML metadata retains the source 65536-byte limit")

	keys := prefix + "iam_saml_idp_keys"
	require.NoError(t, insertFederationSAMLKey(t, db, keys, "Key"))
	require.NoError(t, insertFederationSAMLKey(t, db, keys, "key"), "SAML key IDs use binary comparison")
	assert.Error(t, insertFederationSAMLKey(t, db, keys, strings.Repeat("x", 129)))
	var absent int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN rotated_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE key_id = 'Key'", keys)).Scan(&absent))
	assert.Equal(t, 1, absent)
}

func assertFederationProviderDefaults(t *testing.T, db *sql.DB, providers string) {
	t.Helper()
	var defaults int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN default_role_id IS NULL AND client_secret_ciphertext = '' AND scopes = 'openid profile email' AND auto_provision = TRUE THEN 1 ELSE 0 END FROM %s WHERE id = 1",
		providers)).Scan(&defaults))
	assert.Equal(t, 1, defaults)
}

func verifyFederationRollbackGraph(
	t *testing.T,
	sqlDB *sql.DB,
	db *gorm.DB,
	modules []module.Module,
	dialect, prefix string,
) {
	t.Helper()
	_, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: identity.Name, To: 0,
	}, prefix)
	assert.ErrorContains(t, err, `"federation.constraints"`)

	_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: federation.Name, To: 0,
	}, prefix)
	assert.ErrorContains(t, err, "binding trust downgrade is forbidden")
	assertTableExists(t, sqlDB, dialect, prefix+"iam_identity_providers", true)
	assertTableExists(t, sqlDB, dialect, prefix+"iam_federation_replay_claims", true)
}

func seedFederationPrincipal(t *testing.T, db *sql.DB, prefix string, id int, login string) {
	t.Helper()
	require.NoError(t, execFederationSQL(t, db,
		"INSERT INTO %siam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (%d, '%s', 'Federated User', 'active', '%s', '%s')",
		prefix, id, login, federationTestTime, federationTestTime))
}

func insertFederationProvider(
	t *testing.T,
	db *sql.DB,
	prefix string,
	id, tenantID int,
	name, issuer, clientID, providerType, status string,
) error {
	t.Helper()
	return execFederationSQL(t, db,
		"INSERT INTO %siam_identity_providers (id, tenant_id, name, provider_type, issuer, client_id, status, created_at, updated_at) VALUES (%d, %d, '%s', '%s', '%s', '%s', '%s', '%s', '%s')",
		prefix, id, tenantID, name, providerType, issuer, clientID, status, federationTestTime, federationTestTime)
}

func insertFederationLink(
	t *testing.T,
	db *sql.DB,
	prefix string,
	providerID, tenantID, principalID int,
	externalSubject string,
) error {
	t.Helper()
	return execFederationSQL(t, db,
		"INSERT INTO %siam_identity_links (provider_id, tenant_id, principal_id, external_subject, last_login_at, created_at, updated_at) VALUES (%d, %d, %d, '%s', '%s', '%s', '%s')",
		prefix, providerID, tenantID, principalID, externalSubject, federationTestTime, federationTestTime, federationTestTime)
}

func insertFederationSAMLSP(t *testing.T, db *sql.DB, table string, id int, entityID, metadata, status string) error {
	t.Helper()
	return execFederationSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, entity_id, metadata_xml, status, created_at, updated_at) VALUES (%d, 1, 'Service Provider', '%s', '%s', '%s', '%s', '%s')",
		table, id, entityID, metadata, status, federationTestTime, federationTestTime)
}

func insertFederationSAMLKey(t *testing.T, db *sql.DB, table, keyID string) error {
	t.Helper()
	return execFederationSQL(t, db,
		"INSERT INTO %s (key_id, cert_pem, key_ciphertext, created_at) VALUES ('%s', 'certificate', 'ciphertext', '%s')",
		table, keyID, federationTestTime)
}

func execFederationSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}
