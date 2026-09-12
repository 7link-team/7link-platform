package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindForeignTables publishes the owners' sets into a repository the way
// injection does, for a harness that builds one outside the module. The sets
// come from the owners themselves rather than from a list repeated here, so a
// table an owner stops publishing fails this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(core.TableSet(prefix), organization.TableSet(prefix)))
}

func TestForeignTablesResolveWhatTheOwnersPublished(t *testing.T) {
	tables := &foreignTables{}
	require.NoError(t, tables.bind(core.TableSet("sl_"), organization.TableSet("sl_")))

	roles, err := tables.accessTable("iam_roles")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_roles", roles)
	members, err := tables.accessTable("iam_tenant_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenant_members", members)
	tenants, err := tables.directoryTable("iam_tenants")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", tenants)
	groups, err := tables.directoryTable("iam_groups")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_groups", groups)
}

func TestForeignTablesReportUnboundAndIncompletePublications(t *testing.T) {
	unbound := &foreignTables{}
	for name, lookup := range map[string]func(string) (string, error){
		"core":         unbound.accessTable,
		"organization": unbound.directoryTable,
	} {
		_, err := lookup("iam_tenant_members")
		assert.ErrorIs(t, err, errRepositoryUnavailable, name)
		assert.Contains(t, err.Error(), name+" has not published its table names")
	}

	// A set that arrived without the name this module reads is an error too:
	// a published-but-incomplete set must not degrade into an empty name.
	partial := &foreignTables{}
	require.NoError(t, partial.bind(
		gormx.NewTableSet(map[string]gormx.TableName{"iam_roles": gormx.NewTableName("sl_", "iam_roles")}),
		organization.TableSet("sl_"),
	))
	_, err := partial.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bind(core.TableSet("sl_"), organization.TableSet("sl_")), errRepositoryUnavailable)
	for _, lookup := range []func(string) (string, error){absent.accessTable, absent.directoryTable} {
		_, err := lookup("iam_tenant_members")
		assert.ErrorIs(t, err, errRepositoryUnavailable)
	}
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

// partialForeign binds what both owners publish minus the one name under test,
// so exactly the statement's lookup for that name fails.
func partialForeign(t *testing.T, prefix, owner, omitted string) *foreignTables {
	t.Helper()
	access, directory := core.TableSet(prefix), organization.TableSet(prefix)
	switch owner {
	case "core":
		access = partialSet(access, omitted)
	case "organization":
		directory = partialSet(directory, omitted)
	default:
		t.Fatalf("unknown owner %s", owner)
	}
	tables := &foreignTables{}
	require.NoError(t, tables.bind(access, directory))
	return tables
}

// TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing walks every
// statement that reads a table governance does not own and removes exactly the
// one name it needs. Each must report the gap instead of querying an empty
// table name, and the guarded delete must leave storage untouched.
func TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	seedReviewableGrants(t, h)

	reviewable := func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
		_, err := repo.listReviewableGrants(ctx, 1, governanceTestNow)
		return err
	}
	dynamic := func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
		_, err := repo.listDynamicGroupGrants(ctx, 1)
		return err
	}
	relationship := func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
		_, err := repo.listRelationshipGrants(ctx, 1)
		return err
	}

	for _, testCase := range []struct {
		name    string
		owner   string
		omitted string
		call    func(context.Context, *governanceHarness, *Repository) error
	}{
		{"reviewable role members", "core", "iam_role_members", reviewable},
		{"reviewable roles", "core", "iam_roles", reviewable},
		{"reviewable tenant members", "core", "iam_tenant_members", reviewable},
		{"reviewable temporary grants", "core", "iam_temporary_role_grants", reviewable},
		{"reviewable role bindings", "core", "iam_role_bindings", reviewable},
		{"reviewable entities", "core", "iam_entities", reviewable},
		{"reviewable group role bindings", "organization", "iam_group_role_bindings", reviewable},
		{"reviewable groups", "organization", "iam_groups", reviewable},
		{"reviewable group members", "organization", "iam_group_members", reviewable},
		{"reviewable position role bindings", "organization", "iam_position_role_bindings", reviewable},
		{"reviewable positions", "organization", "iam_positions", reviewable},
		{"reviewable position members", "organization", "iam_position_members", reviewable},
		// The reviewable listing folds in the two statements below it, so a gap
		// only they read has to travel back out rather than be swallowed.
		{"reviewable dynamic member departments", "organization", "iam_member_departments", reviewable},
		{"reviewable relationship grants", "core", "iam_relationships", reviewable},
		{"dynamic groups", "organization", "iam_groups", dynamic},
		{"dynamic group role bindings", "organization", "iam_group_role_bindings", dynamic},
		{"dynamic member departments", "organization", "iam_member_departments", dynamic},
		{"dynamic roles", "core", "iam_roles", dynamic},
		{"dynamic tenant members", "core", "iam_tenant_members", dynamic},
		{"relationship graph", "core", "iam_relationships", relationship},
		{"relationship resource", "core", "iam_resource_relationships", relationship},
		{"relationship entities", "core", "iam_entities", relationship},
		{"relationship tenant members", "core", "iam_tenant_members", relationship},
		{"requestable role", "core", "iam_roles", func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
			_, err := repo.requestableRole(ctx, 1, 20)
			return err
		}},
		{"requestable roles listing", "core", "iam_roles", func(ctx context.Context, h *governanceHarness, repo *Repository) error {
			service := *h.service
			service.repo = repo
			_, err := service.RequestableRoles(ctx, 1)
			return err
		}},
		{"requester membership", "core", "iam_tenant_members", func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
			_, err := repo.activeTenantMember(ctx, 1, 11)
			return err
		}},
		{"requester tenant", "organization", "iam_tenants", func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
			_, err := repo.activeTenantMember(ctx, 1, 11)
			return err
		}},
		{"reviewed graph relationship", "core", "iam_relationships", func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
			_, err := repo.removeRelationship(ctx, 1, 11, 50)
			return err
		}},
		{"reviewed resource relationship", "core", "iam_resource_relationships", func(ctx context.Context, _ *governanceHarness, repo *Repository) error {
			_, err := repo.removeRelationship(ctx, 1, 11, 50)
			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := h.service.repo.withDB(h.db, false)
			repo.foreign = partialForeign(t, h.prefix, testCase.owner, testCase.omitted)

			err := testCase.call(t.Context(), h, repo)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+testCase.omitted)
		})
	}

	// The only guarded write among them is a delete, so the seeded rows it
	// would have removed must still be there once every case has run.
	for _, table := range []string{"iam_relationships", "iam_resource_relationships"} {
		var count int64
		require.NoError(t, h.db.Table(h.table(table)).Count(&count).Error)
		assert.Equal(t, int64(1), count, table)
	}
}
