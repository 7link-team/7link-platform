package core

import (
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// countBootstrapRows reads one tenant-scoped table straight from the database,
// because the point of these tests is what a tenant's first administrator will
// actually find, not what the repository believes it wrote.
func countBootstrapRows(t *testing.T, db *gorm.DB, prefix, table string, tenantID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table(prefix+table).Where("tenant_id = ?", tenantID).Count(&count).Error)
	return count
}

// TestBootstrapTenantMakesTheTenantAdministrableByItsOwner is the whole reason
// the capability exists: every tenant route requires an active membership, so a
// tenant that commits without one cannot be repaired over HTTP.
func TestBootstrapTenantMakesTheTenantAdministrableByItsOwner(t *testing.T) {
	const prefix = "tenantboot_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID, ownerID guid.ID = 6100, 6101
	seedTenantAndPrincipal(t, harness.db, prefix, tenantID, ownerID)

	unclaimed, err := harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.True(t, unclaimed, "a tenant row on its own carries neither a member nor a role")

	result, err := harness.repository.BootstrapTenantOn(t.Context(), harness.db, TenantBootstrapInput{
		TenantID: tenantID, OwnerID: ownerID,
		DisplayName: "  Tenant Owner  ", Email: "  owner@example.test  ",
	}, repositoryTestTime)
	require.NoError(t, err)
	assert.NotZero(t, result.AdministratorRoleID)
	assert.Equal(t, 1, result.PermissionCount)
	assert.Len(t, DefaultMenus(), result.MenuCount)

	var member struct {
		DisplayName string
		Email       string
		Status      string
	}
	require.NoError(t, harness.db.Table(prefix+"iam_tenant_members").
		Select("display_name, email, status").
		Where("tenant_id = ? AND principal_id = ?", tenantID, ownerID).
		Scan(&member).Error)
	assert.Equal(t, "Tenant Owner", member.DisplayName, "the owner profile is trimmed before it reaches the member list")
	assert.Equal(t, "owner@example.test", member.Email)
	assert.Equal(t, string(MemberActive), member.Status)

	var role struct {
		Name    string
		NameKey string
	}
	require.NoError(t, harness.db.Table(prefix+"iam_roles").Select("name, name_key").
		Where("tenant_id = ? AND id = ?", tenantID, result.AdministratorRoleID).Scan(&role).Error)
	assert.Equal(t, administratorRoleName, role.Name)
	assert.Equal(t, roleNameKey(administratorRoleName), role.NameKey,
		"the built-in role goes through the same normalization a renamed role does")

	var permission string
	require.NoError(t, harness.db.Table(prefix+"iam_role_permissions").Select("permission_code").
		Where("tenant_id = ? AND role_id = ?", tenantID, result.AdministratorRoleID).Scan(&permission).Error)
	assert.Equal(t, tenantAdministerPermission, permission)

	assert.EqualValues(t, 1, countBootstrapRows(t, harness.db, prefix, "iam_role_members", tenantID),
		"the owner is in the administrator role, and nobody else is")
	assert.EqualValues(t, len(DefaultMenus()), countBootstrapRows(t, harness.db, prefix, "iam_menus", tenantID))

	unclaimed, err = harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.False(t, unclaimed, "a bootstrapped tenant is claimed, so the repair command must leave it alone")
}

// TestBootstrapTenantRollsBackWithTheCallersTransaction is the property tenant
// creation relies on: a tenant and the administration it needs commit together
// or not at all.
func TestBootstrapTenantRollsBackWithTheCallersTransaction(t *testing.T) {
	const prefix = "tenantbootrollback_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID, ownerID guid.ID = 6200, 6201
	seedTenantAndPrincipal(t, harness.db, prefix, tenantID, ownerID)

	err := harness.db.Transaction(func(tx *gorm.DB) error {
		_, err := harness.repository.BootstrapTenantOn(t.Context(), tx, TenantBootstrapInput{
			TenantID: tenantID, OwnerID: ownerID, DisplayName: "Tenant Owner",
		}, repositoryTestTime)
		require.NoError(t, err)
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)

	for _, table := range []string{"iam_tenant_members", "iam_roles", "iam_role_permissions", "iam_role_members", "iam_menus"} {
		assert.Zerof(t, countBootstrapRows(t, harness.db, prefix, table, tenantID),
			"%s must roll back with the tenant that was never created", table)
	}
	unclaimed, err := harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.True(t, unclaimed)
}

// TestBootstrapTenantNamesAnOwnerItWasGivenNoNameFor keeps the member list
// readable when the owner principal has no display name yet.
func TestBootstrapTenantNamesAnOwnerItWasGivenNoNameFor(t *testing.T) {
	const prefix = "tenantbootname_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID, ownerID guid.ID = 6300, 6301
	seedTenantAndPrincipal(t, harness.db, prefix, tenantID, ownerID)

	_, err := harness.repository.BootstrapTenantOn(t.Context(), harness.db, TenantBootstrapInput{
		TenantID: tenantID, OwnerID: ownerID, DisplayName: "   ",
	}, repositoryTestTime)
	require.NoError(t, err)

	var displayName string
	require.NoError(t, harness.db.Table(prefix+"iam_tenant_members").Select("display_name").
		Where("tenant_id = ?", tenantID).Scan(&displayName).Error)
	assert.Equal(t, ownerID.String(), displayName)
}

// TestBootstrapTenantRefusesInputItCannotHonor keeps a half-administrable
// tenant from being written at all.
func TestBootstrapTenantRefusesInputItCannotHonor(t *testing.T) {
	const prefix = "tenantbootinput_"
	harness := newRepositoryHarness(t, prefix)

	for name, input := range map[string]TenantBootstrapInput{
		"no tenant":        {OwnerID: 6401},
		"no owner":         {TenantID: 6400},
		"unstorable name":  {TenantID: 6400, OwnerID: 6401, DisplayName: strings.Repeat("n", 129)},
		"unstorable email": {TenantID: 6400, OwnerID: 6401, Email: strings.Repeat("e", 321)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := harness.repository.BootstrapTenantOn(t.Context(), harness.db, input, repositoryTestTime)
			assert.ErrorIs(t, err, ErrInvalidArgument)
		})
	}

	_, err := harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)

	var absent *Repository
	_, err = absent.BootstrapTenantOn(t.Context(), harness.db, TenantBootstrapInput{TenantID: 1, OwnerID: 2}, repositoryTestTime)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = harness.repository.BootstrapTenantOn(t.Context(), nil, TenantBootstrapInput{TenantID: 1, OwnerID: 2}, repositoryTestTime)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = absent.TenantIsUnclaimedOn(t.Context(), harness.db, 1)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = harness.repository.TenantIsUnclaimedOn(t.Context(), nil, 1)
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestBootstrapTenantSurfacesStorageFailures keeps a missing table from reading
// as a tenant that was successfully handed to its owner.
func TestBootstrapTenantSurfacesStorageFailures(t *testing.T) {
	for index, table := range []string{"iam_tenant_members", "iam_roles", "iam_role_permissions", "iam_menus"} {
		t.Run(table, func(t *testing.T) {
			// Goose bounds a table prefix at 21 characters, so the per-case
			// schema is numbered rather than named after the dropped table.
			prefix := fmt.Sprintf("tenantbootbroken%d_", index)
			harness := newRepositoryHarness(t, prefix)
			const tenantID, ownerID guid.ID = 6500, 6501
			seedTenantAndPrincipal(t, harness.db, prefix, tenantID, ownerID)
			require.NoError(t, harness.db.Exec("DROP TABLE "+prefix+table).Error)

			_, err := harness.repository.BootstrapTenantOn(t.Context(), harness.db, TenantBootstrapInput{
				TenantID: tenantID, OwnerID: ownerID, DisplayName: "Tenant Owner",
			}, repositoryTestTime)
			assert.Error(t, err)

			if table == "iam_tenant_members" {
				_, err = harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, tenantID)
				assert.Error(t, err, "an unreadable table must not be reported as an unclaimed tenant")
			}
		})
	}

	t.Run("unreadable roles", func(t *testing.T) {
		// The membership table is intact and empty here, so the claim check
		// has to reach the roles table before it can answer.
		const prefix = "tenantbootnoroles_"
		harness := newRepositoryHarness(t, prefix)
		require.NoError(t, harness.db.Exec("DROP TABLE "+prefix+"iam_roles").Error)

		_, err := harness.repository.TenantIsUnclaimedOn(t.Context(), harness.db, 6501)
		assert.Error(t, err)
	})
}

// TestTenantBootstrapperReportsUnavailableBeforeWiring covers the guards inside
// the closures, which are provided before InjectServices supplies the
// repository they need.
func TestTenantBootstrapperReportsUnavailableBeforeWiring(t *testing.T) {
	const prefix = "tenantbootunwired_"
	harness := newRepositoryHarness(t, prefix)
	for name, unwired := range map[string]*Module{
		"nil module":    nil,
		"no repository": {},
	} {
		t.Run(name, func(t *testing.T) {
			capability := unwired.tenantBootstrapper()

			_, err := capability.Bootstrap(t.Context(), harness.db, TenantBootstrapInput{TenantID: 1, OwnerID: 2}, repositoryTestTime)
			assert.ErrorIs(t, err, ErrUnavailable)

			_, err = capability.IsUnclaimed(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, ErrUnavailable)

			assert.ErrorIs(t,
				capability.BumpRevision(t.Context(), harness.db, 1, repositoryTestTime),
				ErrUnavailable)
		})
	}
}

// TestTenantBootstrapperDelegatesToTheWiredRepository proves the capability the
// organization module resolves is the same write path exercised above.
func TestTenantBootstrapperDelegatesToTheWiredRepository(t *testing.T) {
	const prefix = "tenantbootwired_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID, ownerID guid.ID = 6600, 6601
	seedTenantAndPrincipal(t, harness.db, prefix, tenantID, ownerID)
	capability := (&Module{repository: harness.repository}).tenantBootstrapper()

	unclaimed, err := capability.IsUnclaimed(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	require.True(t, unclaimed)

	result, err := capability.Bootstrap(t.Context(), harness.db, TenantBootstrapInput{
		TenantID: tenantID, OwnerID: ownerID, DisplayName: "Tenant Owner",
	}, repositoryTestTime)
	require.NoError(t, err)
	assert.NotZero(t, result.AdministratorRoleID)

	unclaimed, err = capability.IsUnclaimed(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.False(t, unclaimed)

	// The caller bumps the revision in the same transaction as the grant, so
	// the capability has to reach the same store the authorizer reads.
	require.NoError(t, capability.BumpRevision(t.Context(), harness.db, tenantID, repositoryTestTime))
	revision, err := harness.repository.revisions.Current(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, revision)
}

// TestBumpPolicyRevisionTellsCachesTheAnswerChanged covers the third write the
// repair performs. Granting a membership and a role changes what the authorizer
// must answer, and a cache still holding the previous answer would leave the
// repaired owner locked out of the tenant they were just given.
func TestBumpPolicyRevisionTellsCachesTheAnswerChanged(t *testing.T) {
	const prefix = "tenantbootrevision_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID guid.ID = 6700

	before, err := harness.repository.revisions.Current(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	require.Zero(t, before, "a tenant nobody has administered yet has no revision to invalidate")

	require.NoError(t, harness.repository.BumpPolicyRevisionOn(
		t.Context(), harness.db, tenantID, repositoryTestTime,
	))

	after, err := harness.repository.revisions.Current(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, after)
}

// TestBumpPolicyRevisionRollsBackWithTheCallersTransaction keeps the
// invalidation honest. A revision that survived a rolled-back repair would tell
// every cache to re-read a grant that was never written.
func TestBumpPolicyRevisionRollsBackWithTheCallersTransaction(t *testing.T) {
	const prefix = "tenantbootrevroll_"
	harness := newRepositoryHarness(t, prefix)
	const tenantID guid.ID = 6800

	err := harness.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, harness.repository.BumpPolicyRevisionOn(t.Context(), tx, tenantID, repositoryTestTime))
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)

	current, err := harness.repository.revisions.Current(t.Context(), harness.db, tenantID)
	require.NoError(t, err)
	assert.Zero(t, current)
}

// TestBumpPolicyRevisionRefusesWhatItCannotInvalidate keeps a caller from
// reading a silent no-op as a completed invalidation.
func TestBumpPolicyRevisionRefusesWhatItCannotInvalidate(t *testing.T) {
	const prefix = "tenantbootrevbad_"
	harness := newRepositoryHarness(t, prefix)

	assert.ErrorIs(t,
		harness.repository.BumpPolicyRevisionOn(t.Context(), harness.db, 0, repositoryTestTime),
		ErrInvalidArgument)

	var absent *Repository
	assert.ErrorIs(t,
		absent.BumpPolicyRevisionOn(t.Context(), harness.db, 1, repositoryTestTime),
		ErrUnavailable)
	assert.ErrorIs(t,
		harness.repository.BumpPolicyRevisionOn(t.Context(), nil, 1, repositoryTestTime),
		ErrUnavailable)
	assert.ErrorIs(t,
		(&Repository{}).BumpPolicyRevisionOn(t.Context(), harness.db, 1, repositoryTestTime),
		ErrUnavailable)

	require.NoError(t, harness.db.Exec("DROP TABLE "+prefix+"iam_policy_revisions").Error)
	assert.Error(t,
		harness.repository.BumpPolicyRevisionOn(t.Context(), harness.db, 1, repositoryTestTime),
		"an unwritable revision table must not read as an invalidated cache")
}

// TestTenantBootstrapperSystemCapabilitiesGuardWiringAndReachTheSystemRoot
// covers the three system-root closures the organization module calls while it
// creates and upgrades the system root.
func TestTenantBootstrapperSystemCapabilitiesGuardWiringAndReachTheSystemRoot(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	for name, unwired := range map[string]*Module{"nil module": nil, "no repository": {}} {
		capability := unwired.tenantBootstrapper()
		_, err := capability.SystemPermissionOn(t.Context(), h.db, "system_menu_view", 11)
		assert.ErrorIs(t, err, ErrUnavailable, name)
		assert.ErrorIs(t, capability.UpgradeSystem(t.Context(), h.db, 99, repositoryTestTime), ErrUnavailable, name)
		assert.ErrorIs(t, capability.SeedSystemMenus(t.Context(), h.db, 99, repositoryTestTime), ErrUnavailable, name)
	}
	capability := (&Module{repository: h.repository, service: h.service, appender: h.service.writes.audit}).tenantBootstrapper()
	assert.ErrorIs(t, capability.UpgradeSystem(t.Context(), nil, 99, repositoryTestTime), ErrUnavailable)
	assert.ErrorIs(t, capability.SeedSystemMenus(t.Context(), h.db, 0, repositoryTestTime), ErrUnavailable)
	assert.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_menus", 99), "refused calls seed nothing")

	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return capability.SeedSystemMenus(t.Context(), tx, 99, repositoryTestTime)
	}))
	assert.EqualValues(t, len(h.repository.systemMenuDefaults()), countBootstrapRows(t, h.db, "sl_", "iam_menus", 99))
	revision, err := h.repository.revisions.Current(t.Context(), h.db, 99)
	require.NoError(t, err)
	assert.EqualValues(t, 1, revision, "seeding menus invalidates cached navigation")

	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return capability.UpgradeSystem(t.Context(), tx, 99, repositoryTestTime)
	}))
	roles, err := h.repository.ListRoles(t.Context(), 99)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, SystemSuperadministratorRoleName, roles[0].Name, "the upgrade installs the builtin full-authority role")
	_, err = capability.SystemPermissionOn(t.Context(), h.db, "system_menu_view", 11)
	assert.ErrorIs(t, err, ErrInvalidArgument, "system authority is only evaluated inside the caller's transaction")
	permission := func(subject guid.ID) (allowed bool) {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			allowed, err = capability.SystemPermissionOn(t.Context(), tx, "system_menu_view", subject)
			return err
		}))
		return allowed
	}
	assert.True(t, permission(11), "a full administrator holds every system permission")
	assert.False(t, permission(33))
}
