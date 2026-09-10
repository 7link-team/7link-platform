package migration_test

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var createIAMTablePattern = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+\{\{\.Prefix\}\}(iam_[a-z0-9_]+)`)

type iamMigrationContract struct {
	name   string
	tables []string
	module module.Module
}

func TestIAMMigrationOwnershipIsAutonomousAndComplete(t *testing.T) {
	contracts := iamMigrationContracts()
	owners := make(map[string]string, 72)
	versionTables := make(map[string]string, len(contracts))
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			provider, ok := contract.module.(module.MigrationSourceProvider)
			require.True(t, ok, "%s must own a migration source", contract.name)
			source := provider.MigrationSource()
			assert.NotEmpty(t, source.TableName)
			if previous, duplicate := versionTables[source.TableName]; duplicate {
				t.Errorf("modules %s and %s share migration version table %s", previous, contract.name, source.TableName)
			}
			versionTables[source.TableName] = contract.name

			sets := make(map[string][]string, 3)
			for _, dialect := range []string{"postgres", "mysql", "sqlite"} {
				sets[dialect] = iamMigrationTables(t, source, dialect)
				assert.Equal(t, contract.tables, sets[dialect], "%s/%s table ownership", contract.name, dialect)
			}
			assert.Equal(t, sets["postgres"], sets["mysql"], "%s postgres/mysql table set", contract.name)
			assert.Equal(t, sets["postgres"], sets["sqlite"], "%s postgres/sqlite table set", contract.name)
			for _, table := range sets["postgres"] {
				if previous, duplicate := owners[table]; duplicate {
					t.Errorf("table %s is owned by both %s and %s", table, previous, contract.name)
				}
				owners[table] = contract.name
			}
		})
	}
	assert.Len(t, owners, 72)
}

func iamMigrationContracts() []iamMigrationContract {
	return []iamMigrationContract{
		{name: "iam", tables: []string{
			"iam_entities", "iam_field_policies", "iam_menus", "iam_platform_administrators", "iam_platform_grants",
			"iam_policy_revisions", "iam_relationships", "iam_resource_relationships", "iam_role_bindings",
			"iam_role_members", "iam_role_permissions", "iam_roles", "iam_temporary_role_grants", "iam_tenant_members",
		}, module: &iamcore.Module{}},
		{name: "authn", tables: []string{
			"iam_credentials", "iam_email_verification_tokens", "iam_mfa_challenges", "iam_mfa_enrollments",
			"iam_notification_outbox", "iam_passkey_challenges", "iam_passkey_users", "iam_passkeys",
			"iam_password_history", "iam_password_recovery_tokens", "iam_personal_tokens", "iam_reauth_challenges", "iam_recovery_codes", "iam_sessions",
			"iam_stepup_challenges",
		}, module: &authn.Module{}},
		{name: "identity", tables: []string{
			"iam_closure_requests", "iam_principal_lifecycles", "iam_principals", "iam_service_account_credentials", "iam_service_accounts",
		}, module: &identity.Module{}},
		{name: "oauth", tables: []string{
			"iam_oauth_clients", "iam_oauth_codes", "iam_oauth_consent_requests", "iam_oauth_consents",
			"iam_refresh_tokens",
		}, module: &oauth.Module{}},
		{name: "audit", tables: []string{
			"iam_audit_archive_jobs", "iam_audit_events", "iam_audit_heads", "iam_audit_retention_policies",
		}, module: &iamaudit.Module{}},
		{name: "organization", tables: []string{
			"iam_department_closure", "iam_departments", "iam_group_members", "iam_group_role_bindings",
			"iam_groups", "iam_invitation_roles", "iam_invitations", "iam_member_departments",
			"iam_position_members", "iam_position_role_bindings", "iam_positions", "iam_role_data_scopes",
			"iam_role_scope_departments", "iam_tenants",
		}, module: &organization.Module{}},
		{name: "governance", tables: []string{
			"iam_access_requests", "iam_access_review_items", "iam_access_reviews", "iam_approval_steps",
		}, module: &governance.Module{}},
		{name: "provisioning", tables: []string{
			"iam_scim_credentials", "iam_scim_directories", "iam_scim_resources", "iam_scim_target_resources", "iam_scim_targets",
		}, module: &provisioning.Module{}},
		{name: "federation", tables: []string{
			"iam_federation_replay_claims", "iam_identity_link_transactions", "iam_identity_links", "iam_identity_providers", "iam_saml_idp_keys", "iam_saml_service_providers",
		}, module: &federation.Module{}},
	}
}

func iamMigrationTables(t *testing.T, source goosex.Source, dialect string) []string {
	t.Helper()
	_, subdirectory, err := goosex.ResolveDialect(dialect)
	require.NoError(t, err)
	root, err := fs.Sub(source.FS, subdirectory)
	require.NoError(t, err)
	tables := make(map[string]struct{})
	require.NoError(t, fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".sql" || strings.Contains(entry.Name(), "constraints") {
			return walkErr
		}
		contents, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return readErr
		}
		for _, match := range createIAMTablePattern.FindAllSubmatch(contents, -1) {
			tables[strings.ToLower(string(match[1]))] = struct{}{}
		}
		return nil
	}))
	result := make([]string, 0, len(tables))
	for table := range tables {
		result = append(result, table)
	}
	sort.Strings(result)
	return result
}
