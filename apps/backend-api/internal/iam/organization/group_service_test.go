package organization

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// grantingAdministratorGuard adds the optional grant capabilities core's
// administrator guard publishes, recording what the group service asked for.
type grantingAdministratorGuard struct {
	organizationAdministratorGuard
	prepareErr error
	requireErr error
	prepared   []guid.ID
	required   [][]guid.ID
	placeErr   error
	placed     []guid.ID
	moveErr    error
	moved      []guid.ID
}

func (g *grantingAdministratorGuard) RequireGrantableDepartmentMove(_ context.Context, _ *gorm.DB, owner guid.ID) error {
	g.moved = append(g.moved, owner)
	return g.moveErr
}

func (g *grantingAdministratorGuard) RequireGrantableDepartmentPlacement(_ context.Context, _ *gorm.DB, _, department guid.ID, _ []guid.ID) error {
	g.placed = append(g.placed, department)
	return g.placeErr
}

func (g *grantingAdministratorGuard) PrepareGrantableRoles(_ context.Context, _ *gorm.DB, owner guid.ID) error {
	g.prepared = append(g.prepared, owner)
	return g.prepareErr
}

func (g *grantingAdministratorGuard) RequireGrantableRoles(_ context.Context, _ *gorm.DB, _ guid.ID, roles []guid.ID) error {
	g.required = append(g.required, roles)
	return g.requireErr
}

func (h *organizationHarness) groupMemberCount(t *testing.T, owner, groupID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_group_members")).Where("tenant_id = ? AND group_id = ?", owner, groupID).Count(&count).Error)
	return count
}

func TestGroupMembershipFollowsTheOwnerKindOfTheGroup(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(77,'platform','operator','Operator','active',1,?,?),(99,'system',?,'System','active',1,?,?)", organizationTestNow, organizationTestNow, core.SystemRootSlug, organizationTestNow, organizationTestNow).Error)
	require.NoError(t, h.db.Exec("CREATE TABLE sl_iam_principals (id BIGINT PRIMARY KEY, status TEXT NOT NULL)").Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_principals(id,status) VALUES(5,'active'),(6,'active'),(7,'disabled')").Error)
	h.seedMember(t, 77, 5, "Operator")
	h.seedMember(t, 1, 6, "Tenant only")
	h.seedMember(t, 77, 7, "Disabled operator")
	h.seedMember(t, 99, 5, "System member")
	members := core.NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(CoreDirectoryTableNames(h.prefix), core.PrincipalTableNames{Principals: gormx.NewTableName(h.prefix, "iam_principals")}))
	groups := newBoundGroupService(t, h.runtime, h.appender, members, &organizationAdministratorGuard{}, newOrganizationTestIDs(400))

	platformGroup, err := groups.Create(t.Context(), 77, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	member, err := groups.PutMember(t.Context(), 77, platformGroup.ID, 5, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Equal(t, guid.ID(5), member.PrincipalID)
	for _, principal := range []guid.ID{6, 7} {
		_, err = groups.PutMember(t.Context(), 77, platformGroup.ID, principal, GroupMemberWindow{})
		require.ErrorIs(t, err, ErrGroupMemberInactive, "principal %d is not an active operator of the platform", principal)
	}
	assert.Equal(t, int64(1), h.groupMemberCount(t, 77, platformGroup.ID))

	tenantGroup, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Tenant staff"})
	require.NoError(t, err)
	_, err = groups.PutMember(t.Context(), 1, tenantGroup.ID, 6, GroupMemberWindow{})
	require.NoError(t, err, "tenant groups admit active tenant members")
	_, err = groups.PutMember(t.Context(), 1, tenantGroup.ID, 5, GroupMemberWindow{})
	require.ErrorIs(t, err, ErrGroupMemberInactive, "a platform operator is not a tenant member")

	systemGroup, err := groups.Create(t.Context(), 99, CreateGroup{Name: "System staff"})
	require.NoError(t, err)
	_, err = groups.PutMember(t.Context(), 99, systemGroup.ID, 5, GroupMemberWindow{})
	require.ErrorIs(t, err, ErrGroupMemberInactive, "no directory membership admits principals into a system root group")
	assert.Zero(t, h.groupMemberCount(t, 99, systemGroup.ID))

	// A member checker without the transactional platform capability cannot
	// decide platform membership, so the write fails closed.
	tenantOnly := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{}, newOrganizationTestIDs(500))
	_, err = tenantOnly.PutMember(t.Context(), 77, platformGroup.ID, 7, GroupMemberWindow{})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int64(1), h.groupMemberCount(t, 77, platformGroup.ID))
}

func TestGroupWritesThatWidenAuthorityRequireGrantableBoundRoles(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 1, 11, "Bob")
	h.seedRole(t, 1, 700)
	h.seedRole(t, 1, 701)
	guard := &grantingAdministratorGuard{}
	groups := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, guard, newOrganizationTestIDs(400))
	bound, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Bound"})
	require.NoError(t, err)
	unbound, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Unbound"})
	require.NoError(t, err)
	for _, role := range []guid.ID{700, 701} {
		require.NoError(t, h.db.Table(h.table("iam_group_role_bindings")).Create(map[string]any{
			"tenant_id": 1, "role_id": role, "group_id": bound.ID, "created_at": organizationTestNow,
		}).Error)
	}
	caller := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 1, TenantID: 1})

	// Without a caller there is no grantor whose authority could be exceeded.
	_, err = groups.PutMember(t.Context(), 1, bound.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Empty(t, guard.prepared)
	assert.Empty(t, guard.required)

	_, err = groups.PutMember(caller, 1, unbound.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{1}, guard.prepared)
	assert.Empty(t, guard.required, "a group without role bindings grants nothing")

	guard.requireErr = core.ErrPrivilegeEscalation
	_, err = groups.PutMember(caller, 1, bound.ID, 11, GroupMemberWindow{})
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
	require.Len(t, guard.required, 1)
	assert.ElementsMatch(t, []guid.ID{700, 701}, guard.required[0], "every role the group confers is checked")
	assert.Equal(t, int64(1), h.groupMemberCount(t, 1, bound.ID), "a denied assignment must not be stored")

	guard.requireErr = nil
	_, err = groups.PutMember(caller, 1, bound.ID, 11, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), h.groupMemberCount(t, 1, bound.ID))

	disabled := StatusDisabled
	updated, err := groups.Update(t.Context(), 1, bound.ID, UpdateGroup{Status: &disabled, Version: bound.Version})
	require.NoError(t, err)
	active := StatusActive
	guard.requireErr, guard.required = core.ErrPrivilegeEscalation, nil
	_, err = groups.Update(caller, 1, bound.ID, UpdateGroup{Status: &active, Version: updated.Version})
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation, "re-enabling a group re-grants its roles")
	require.Len(t, guard.required, 1)
	stored, err := groups.Get(t.Context(), 1, bound.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, stored.Status)
	assert.Equal(t, updated.Version, stored.Version)

	guard.requireErr = nil
	guard.prepareErr = errors.New("grant snapshot unavailable")
	_, err = groups.Update(caller, 1, bound.ID, UpdateGroup{Status: &active, Version: updated.Version})
	require.ErrorIs(t, err, guard.prepareErr)
	stored, err = groups.Get(t.Context(), 1, bound.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, stored.Status)
}

func TestGroupGrantCheckFailsClosedWithoutTheGrantCapability(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	h.seedRole(t, 1, 700)
	groups := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{}, newOrganizationTestIDs(400))
	bound, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Bound"})
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_group_role_bindings")).Create(map[string]any{
		"tenant_id": 1, "role_id": 700, "group_id": bound.ID, "created_at": organizationTestNow,
	}).Error)
	caller := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 1, TenantID: 1})
	_, err = groups.PutMember(caller, 1, bound.ID, 10, GroupMemberWindow{})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Zero(t, h.groupMemberCount(t, 1, bound.ID))
}
