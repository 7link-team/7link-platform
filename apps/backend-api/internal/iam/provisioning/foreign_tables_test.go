package provisioning

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testForeignTables publishes the owners' sets the way injection does. The
// sets come from the owners themselves rather than from a list repeated here,
// so a table an owner stops publishing fails these tests too.
func testForeignTables(t *testing.T, prefix string) *foreignTables {
	t.Helper()
	tables := &foreignTables{}
	require.NoError(t, tables.bind(core.TableSet(prefix), identity.TableSet(prefix), organization.TableSet(prefix)))
	return tables
}

// bindForeignTables publishes the owners' sets into a repository the way
// injection does, for a harness that builds one outside the module.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(
		core.TableSet(prefix), identity.TableSet(prefix), organization.TableSet(prefix),
	))
}

func TestForeignTablesResolveWhatTheOwnersPublished(t *testing.T) {
	tables := testForeignTables(t, "sl_")

	members, err := tables.accessTable("iam_tenant_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenant_members", members)
	principals, err := tables.principalTable("iam_principals")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_principals", principals)
	tenants, err := tables.directoryTable("iam_tenants")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", tenants)
	groups, err := tables.directoryTable("iam_groups")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_groups", groups)
	groupMembers, err := tables.directoryTable("iam_group_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_group_members", groupMembers)
}

func TestForeignTablesReportUnboundAndIncompletePublications(t *testing.T) {
	unbound := &foreignTables{}
	for name, lookup := range map[string]func(string) (string, error){
		"core":         unbound.accessTable,
		"identity":     unbound.principalTable,
		"organization": unbound.directoryTable,
	} {
		_, err := lookup("iam_tenants")
		assert.ErrorIs(t, err, errRepositoryUnavailable, name)
		assert.Contains(t, err.Error(), name+" has not published its table names")
	}

	// A set that arrived without the name this module reads is an error too:
	// a published-but-incomplete set must not degrade into an empty name.
	partial := &foreignTables{}
	require.NoError(t, partial.bind(
		gormx.NewTableSet(map[string]gormx.TableName{"iam_roles": gormx.NewTableName("sl_", "iam_roles")}),
		identity.TableSet("sl_"), organization.TableSet("sl_"),
	))
	_, err := partial.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bind(
		core.TableSet("sl_"), identity.TableSet("sl_"), organization.TableSet("sl_"),
	), errRepositoryUnavailable)
	for _, lookup := range []func(string) (string, error){
		absent.accessTable, absent.principalTable, absent.directoryTable,
	} {
		_, err := lookup("iam_tenants")
		assert.ErrorIs(t, err, errRepositoryUnavailable)
	}
}

// TestServiceBindsPublishedTablesOutsideModuleWiring covers the entry point a
// caller that builds a service itself has to use, because injection is what
// binds the names everywhere else.
func TestServiceBindsPublishedTablesOutsideModuleWiring(t *testing.T) {
	env := newProvisioningEnvironment(t)
	require.NoError(t, env.service.BindForeignTables(
		core.TableSet(env.prefix), identity.TableSet(env.prefix), organization.TableSet(env.prefix),
	))
	_, err := env.service.repo.foreign.directoryTable("iam_tenants")
	require.NoError(t, err)

	var absent *Service
	assert.ErrorIs(t, absent.BindForeignTables(
		core.TableSet(""), identity.TableSet(""), organization.TableSet(""),
	), errRepositoryUnavailable)
}

// partialSet publishes every logical name an owner owns except one, so a
// lookup for the missing table fails while the rest of the statement's names
// still resolve.
func partialSet(full gormx.TableSet, omit string) gormx.TableSet {
	names := make(map[string]gormx.TableName, full.Len())
	for _, logical := range full.Names() {
		if logical == omit {
			continue
		}
		name, _ := full.Lookup(logical)
		names[logical] = name
	}
	return gormx.NewTableSet(names)
}

// TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing walks every
// statement that reads a table provisioning does not own and removes exactly
// the one name it needs. Each must report the gap instead of querying an empty
// table name.
func TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing(t *testing.T) {
	env := newProvisioningEnvironmentWithPrefix(t, "sl_")
	prefix := env.prefix
	full := func() (gormx.TableSet, gormx.TableSet, gormx.TableSet) {
		return core.TableSet(prefix), identity.TableSet(prefix), organization.TableSet(prefix)
	}
	withoutAccess := func(omit string) func(*foreignTables) error {
		return func(tables *foreignTables) error {
			access, principal, directory := full()
			return tables.bind(partialSet(access, omit), principal, directory)
		}
	}
	withoutPrincipal := func(omit string) func(*foreignTables) error {
		return func(tables *foreignTables) error {
			access, principal, directory := full()
			return tables.bind(access, partialSet(principal, omit), directory)
		}
	}
	withoutDirectory := func(omit string) func(*foreignTables) error {
		return func(tables *foreignTables) error {
			access, principal, directory := full()
			return tables.bind(access, principal, partialSet(directory, omit))
		}
	}
	memberFilter := ListRequest{Filter: `members[value eq "` + wireID("principal") + `"]`, StartIndex: 1, Count: 10}

	for _, testCase := range []struct {
		name    string
		omitted string
		bind    func(*foreignTables) error
		call    func(*Repository) error
	}{
		{"credential tenant", "iam_tenants", withoutDirectory("iam_tenants"), func(repo *Repository) error {
			_, err := repo.credentialForAuth(t.Context(), env.auth.CredentialID, env.auth.TenantID, env.auth.DirectoryID)
			return err
		}},
		{"user principal", "iam_principals", withoutPrincipal("iam_principals"), func(repo *Repository) error {
			_, err := repo.user(t.Context(), env.auth.DirectoryID, 1)
			return err
		}},
		{"user member", "iam_tenant_members", withoutAccess("iam_tenant_members"), func(repo *Repository) error {
			_, err := repo.user(t.Context(), env.auth.DirectoryID, 1)
			return err
		}},
		{"user page principal", "iam_principals", withoutPrincipal("iam_principals"), func(repo *Repository) error {
			_, _, err := repo.userPage(t.Context(), env.auth.DirectoryID, ListRequest{StartIndex: 1, Count: 10}, "sqlite")
			return err
		}},
		{"group", "iam_groups", withoutDirectory("iam_groups"), func(repo *Repository) error {
			_, err := repo.group(t.Context(), env.auth.DirectoryID, 1)
			return err
		}},
		{"group page", "iam_groups", withoutDirectory("iam_groups"), func(repo *Repository) error {
			_, _, err := repo.groupPage(t.Context(), env.auth.DirectoryID, ListRequest{StartIndex: 1, Count: 10}, "sqlite")
			return err
		}},
		{"group member filter", "iam_group_members", withoutDirectory("iam_group_members"), func(repo *Repository) error {
			_, _, err := repo.groupPage(t.Context(), env.auth.DirectoryID, memberFilter, "sqlite")
			return err
		}},
		{"group members", "iam_group_members", withoutDirectory("iam_group_members"), func(repo *Repository) error {
			_, err := repo.groupMembers(t.Context(), env.auth.DirectoryID, 1)
			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := env.service.repo.withDB(env.db, false)
			tables := &foreignTables{}
			require.NoError(t, testCase.bind(tables))
			repo.foreign = tables

			err := testCase.call(repo)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+testCase.omitted)
		})
	}
}

// TestWritesRejectedByMissingForeignTablesLeaveStorageUntouched covers the two
// writes that admit a row only after reading the tenant table organization
// owns: an unpublished name must stop them before anything is stored.
func TestWritesRejectedByMissingForeignTablesLeaveStorageUntouched(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		table string
		call  func(*testing.T, *provisioningEnvironment) error
	}{
		{"directory", model.Directory{}.TableName(), func(t *testing.T, env *provisioningEnvironment) error {
			_, err := env.service.CreateDirectory(t.Context(), testID("tenant-a"), "Rejected", nil)
			return err
		}},
		{"target", model.Target{}.TableName(), func(t *testing.T, env *provisioningEnvironment) error {
			_, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Rejected", "https://example.test/scim", "secret", nil)
			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newProvisioningEnvironment(t)
			count := func() int64 {
				var rows int64
				require.NoError(t, env.db.Table(env.prefix+testCase.table).Count(&rows).Error)
				return rows
			}
			before := count()
			tables := &foreignTables{}
			require.NoError(t, tables.bind(
				core.TableSet(env.prefix), identity.TableSet(env.prefix),
				partialSet(organization.TableSet(env.prefix), "iam_tenants"),
			))
			env.service.repo.foreign = tables

			err := testCase.call(t, env)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish iam_tenants")
			assert.Equal(t, before, count(), "a rejected write must not reach storage")
		})
	}
}

// TestReconcilerStopsWhenTheTenantTableWasNeverPublished pins the sync loop to
// the same rule as the request paths: it reports a failure rather than listing
// targets against a name it invented.
func TestReconcilerStopsWhenTheTenantTableWasNeverPublished(t *testing.T) {
	env := newProvisioningEnvironment(t)
	_, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Unpublished", "https://example.test/scim", "secret", nil)
	require.NoError(t, err)
	tables := &foreignTables{}
	require.NoError(t, tables.bind(
		core.TableSet(env.prefix), identity.TableSet(env.prefix),
		partialSet(organization.TableSet(env.prefix), "iam_tenants"),
	))
	env.service.repo.foreign = tables

	instance := &Module{service: env.service}
	assert.NotPanics(t, func() { instance.reconcileAll(t.Context()) })
	assert.EqualValues(t, 1, instance.syncFailures.Load())
	assert.Zero(t, instance.syncCursor, "no target may be claimed from a listing that never ran")
}
