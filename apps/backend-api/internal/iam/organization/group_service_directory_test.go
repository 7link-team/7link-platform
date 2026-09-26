package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type organizationMemberChecker struct {
	prefix string
	err    error
}

func (c *organizationMemberChecker) IsMemberActive(ctx context.Context, tenantID, principalID guid.ID) (bool, error) {
	return false, errors.New("transactional check required")
}

func (c *organizationMemberChecker) IsMemberActiveOn(ctx context.Context, db *gorm.DB, tenantID, principalID guid.ID) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	var count int64
	err := db.WithContext(ctx).Table(c.prefix+"iam_tenant_members").Where("tenant_id = ? AND principal_id = ? AND status = 'active'", tenantID, principalID).Count(&count).Error
	return count == 1, err
}

type organizationAdministratorGuard struct {
	err       error
	verifyErr error
	verified  int
}

func (g *organizationAdministratorGuard) Protect(context.Context, *gorm.DB, guid.ID) (func() error, error) {
	if g.err != nil {
		return nil, g.err
	}
	return func() error { g.verified++; return g.verifyErr }, nil
}

var _ core.ActiveMemberChecker = (*organizationMemberChecker)(nil)
var _ core.AdministratorGuard = (*organizationAdministratorGuard)(nil)

func (h *organizationHarness) seedMember(t *testing.T, tenantID, principalID guid.ID, name string) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(map[string]any{
		"tenant_id": tenantID, "principal_id": principalID, "display_name": name,
		"email": name + "@example.com", "status": StatusActive,
		"created_at": organizationTestNow, "updated_at": organizationTestNow,
	}).Error)
}

func TestGroupLifecycleMembersAndTenantIsolation(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 2, 20, "Bob")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, guard, newOrganizationTestIDs(200))
	service.now = func() time.Time { return organizationTestNow }

	group, err := service.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	other, err := service.Create(t.Context(), 2, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	_, err = service.Get(t.Context(), 1, other.ID)
	assert.ErrorIs(t, err, ErrGroupNotFound)
	_, err = service.Create(t.Context(), 1, CreateGroup{Name: " operators "})
	assert.ErrorIs(t, err, ErrGroupNameConflict)

	_, err = service.PutMember(t.Context(), 1, group.ID, 20, GroupMemberWindow{})
	assert.ErrorIs(t, err, ErrGroupMemberInactive)
	member, err := service.PutMember(t.Context(), 1, group.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Equal(t, "Alice", member.DisplayName)
	revision := h.revision(t, 1)
	_, err = service.PutMember(t.Context(), 1, group.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	assert.Equal(t, revision, h.revision(t, 1), "idempotent membership must not advance policy revision")
	members, err := service.ListMembers(t.Context(), 1, group.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.ErrorIs(t, service.Delete(t.Context(), 1, group.ID, group.Version), ErrGroupHasMembers)
	changed, err := service.DeleteMember(t.Context(), 1, group.ID, 10)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = service.DeleteMember(t.Context(), 1, group.ID, 10)
	require.NoError(t, err)
	assert.False(t, changed)
	require.NoError(t, service.Delete(t.Context(), 1, group.ID, group.Version))
	assert.GreaterOrEqual(t, guard.verified, 3)
}

func TestDynamicGroupEvaluationAndMutationGuard(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 1, 11, "Bob")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{}, guard, newOrganizationTestIDs(300))
	service.now = func() time.Time { return organizationTestNow }
	rule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.email","operator":"in","values":["alice@example.com"]}]}`)
	group, err := service.Create(t.Context(), 1, CreateGroup{Name: "Alice only", Type: GroupTypeDynamic, Rule: rule})
	require.NoError(t, err)
	members, err := service.ListMembers(t.Context(), 1, group.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.EqualValues(t, 10, members[0].PrincipalID)
	_, err = service.PutMember(t.Context(), 1, group.ID, 10, GroupMemberWindow{})
	assert.ErrorIs(t, err, ErrDynamicGroupMembers)
	_, err = service.DeleteMember(t.Context(), 1, group.ID, 10)
	assert.ErrorIs(t, err, ErrDynamicGroupMembers)
}

func TestGroupUpdateFieldsGuardsAndReferenceChecks(t *testing.T) {
	h := newOrganizationHarness(t, "")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{}, guard, newOrganizationTestIDs(1000))
	service.now = func() time.Time { return organizationTestNow }
	rule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`)
	group, err := service.Create(t.Context(), 1, CreateGroup{Name: "Dynamic", Type: GroupTypeDynamic, Rule: rule})
	require.NoError(t, err)

	name, description := "Updated", "Updated description"
	status, sortOrder := StatusDisabled, 9
	updatedRule := MembershipRule(`{"version":1,"match":"any","conditions":[{"field":"member.email","operator":"in","values":["alice@example.com"]}]}`)
	group, err = service.Update(t.Context(), 1, group.ID, UpdateGroup{
		Name: &name, Description: &description, Status: &status, SortOrder: &sortOrder,
		Rule: &updatedRule, Version: group.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, name, group.Name)
	assert.Equal(t, description, group.Description)
	assert.Equal(t, status, group.Status)
	assert.Equal(t, sortOrder, group.SortOrder)
	assert.EqualValues(t, 2, group.Version)
	assert.Equal(t, 1, guard.verified)
	revision := h.revision(t, 1)

	unchanged, err := service.Update(t.Context(), 1, group.ID, UpdateGroup{
		Name: &name, Description: &description, Status: &status, SortOrder: &sortOrder,
		Rule: &updatedRule, Version: group.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, group.Version, unchanged.Version)
	assert.Equal(t, revision, h.revision(t, 1))

	staticGroup, err := service.Create(t.Context(), 1, CreateGroup{Name: "Static"})
	require.NoError(t, err)
	_, err = service.Update(t.Context(), 1, staticGroup.ID, UpdateGroup{Rule: &rule, Version: staticGroup.Version})
	assert.ErrorIs(t, err, ErrGroupRuleType)

	guard.verifyErr = assert.AnError
	_, err = service.Update(t.Context(), 1, staticGroup.ID, UpdateGroup{Status: &status, Version: staticGroup.Version})
	assert.ErrorIs(t, err, assert.AnError)
	stored, err := service.Get(t.Context(), 1, staticGroup.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, stored.Status)
	assert.Equal(t, staticGroup.Version, stored.Version)
	guard.verifyErr = nil

	roleBound, err := service.Create(t.Context(), 1, CreateGroup{Name: "Role bound"})
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_group_role_bindings")).Create(map[string]any{
		"tenant_id": 1, "role_id": 10, "group_id": roleBound.ID, "created_at": organizationTestNow,
	}).Error)
	assert.ErrorIs(t, service.Delete(t.Context(), 1, roleBound.ID, roleBound.Version), ErrGroupRoleBound)

	relationshipBound, err := service.Create(t.Context(), 1, CreateGroup{Name: "Relationship bound"})
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_relationships")).Create(map[string]any{
		"tenant_id": 1, "subject_type": "group", "subject_id": relationshipBound.ID,
	}).Error)
	assert.ErrorIs(t, service.Delete(t.Context(), 1, relationshipBound.ID, relationshipBound.Version), ErrGroupRelationshipBound)
}

func TestGroupAndPositionMembershipFailuresRollback(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 1, 11, "Bob")
	checker := &organizationMemberChecker{}
	guard := &organizationAdministratorGuard{}
	groups := newBoundGroupService(t, h.runtime, h.appender, checker, guard, newOrganizationTestIDs(1200))
	groups.now = func() time.Time { return organizationTestNow }
	group, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
	require.NoError(t, err)

	start := organizationTestNow.Add(time.Hour)
	end := start.Add(time.Hour)
	_, err = groups.PutMember(t.Context(), 1, group.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	updatedGroupMember, err := groups.PutMember(t.Context(), 1, group.ID, 10, GroupMemberWindow{StartsAt: &start, EndsAt: &end})
	require.NoError(t, err)
	assert.Equal(t, organizationTime(start), *updatedGroupMember.StartsAt)

	checker.err = assert.AnError
	_, err = groups.PutMember(t.Context(), 1, group.ID, 11, GroupMemberWindow{})
	assert.ErrorIs(t, err, assert.AnError)
	assert.ErrorIs(t, err, assert.AnError)
	checker.err = nil

	guard.err = assert.AnError
	_, err = groups.DeleteMember(t.Context(), 1, group.ID, 10)
	assert.ErrorIs(t, err, assert.AnError)
	assert.ErrorIs(t, err, assert.AnError)
	guard.err = nil
	guard.verifyErr = assert.AnError
	_, err = groups.DeleteMember(t.Context(), 1, group.ID, 10)
	assert.ErrorIs(t, err, assert.AnError)
	assert.ErrorIs(t, err, assert.AnError)
	groupMembers, err := groups.ListMembers(t.Context(), 1, group.ID)
	require.NoError(t, err)
	assert.Len(t, groupMembers, 1)

	guard.verifyErr = nil
	h.auditErr = assert.AnError
	_, err = groups.PutMember(t.Context(), 1, group.ID, 11, GroupMemberWindow{})
	assert.ErrorIs(t, err, assert.AnError)
	assert.ErrorIs(t, err, assert.AnError)
	groupMembers, err = groups.ListMembers(t.Context(), 1, group.ID)
	require.NoError(t, err)
	assert.Len(t, groupMembers, 1)
}

// TestDirectoryServicesRejectStaleVersions pins the concurrent-edit contract at
// the service boundary. Two editors read version 1; one writes and the row
// moves to 2. The second editor's update and delete both still carry version 1,
// and both have to be refused with the conflict error the API maps to a 409 --
// not applied, not partially applied, and without advancing the policy revision
// that tells the authorization caches something changed.
func TestDirectoryServicesRejectStaleVersions(t *testing.T) {
	h := newOrganizationHarness(t, "")
	guard := &organizationAdministratorGuard{}
	groups := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{}, guard, newOrganizationTestIDs(2200))
	groups.now = func() time.Time { return organizationTestNow }

	group, err := groups.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	require.EqualValues(t, 1, group.Version)
	winnerName := "Operators renamed"
	group, err = groups.Update(t.Context(), 1, group.ID, UpdateGroup{Name: &winnerName, Version: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, group.Version)
	groupRevision := h.revision(t, 1)

	loserName := "Operators clobbered"
	_, err = groups.Update(t.Context(), 1, group.ID, UpdateGroup{Name: &loserName, Version: 1})
	assert.ErrorIs(t, err, ErrGroupVersionConflict)
	assert.ErrorIs(t, groups.Delete(t.Context(), 1, group.ID, 1), ErrGroupVersionConflict)
	storedGroup, err := groups.Get(t.Context(), 1, group.ID)
	require.NoError(t, err)
	assert.Equal(t, winnerName, storedGroup.Name, "the edit that won must survive the stale one")
	assert.EqualValues(t, 2, storedGroup.Version)
	assert.Equal(t, groupRevision, h.revision(t, 1), "a rejected edit must not advance the policy revision")

}

// TestGroupWritesAbortWhenThePolicyRevisionCannotAdvance pins that every group
// and group-membership write is committed together with the tenant's
// authorization policy revision. The revision is what invalidates the caches
// that answer with these memberships, so a membership change that committed
// without it would keep granting access the directory no longer records.
func TestGroupWritesAbortWhenThePolicyRevisionCannotAdvance(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix},
		&organizationAdministratorGuard{}, newOrganizationTestIDs(700),
	)
	service.now = func() time.Time { return organizationTestNow }
	created, err := service.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	_, err = service.PutMember(t.Context(), 1, created.ID, 10, GroupMemberWindow{})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_policy_revisions").Error)

	_, err = service.Create(t.Context(), 1, CreateGroup{Name: "Viewers"})
	assert.Error(t, err)
	_, err = service.Update(t.Context(), 1, created.ID, UpdateGroup{
		Description: stringPointer("Tenant operators"), Version: created.Version,
	})
	assert.Error(t, err)
	endsAt := organizationTestNow.Add(time.Hour)
	_, err = service.PutMember(t.Context(), 1, created.ID, 10, GroupMemberWindow{EndsAt: &endsAt})
	assert.Error(t, err)
	_, err = service.DeleteMember(t.Context(), 1, created.ID, 10)
	assert.Error(t, err)
	assert.Error(t, service.Delete(t.Context(), 1, created.ID, created.Version))

	groups, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, groups, 1, "no group may be added or removed without a revision bump")
	assert.Empty(t, groups[0].Description, "a rejected description change must not survive")
	members, err := service.ListMembers(t.Context(), 1, created.ID)
	require.NoError(t, err)
	require.Len(t, members, 1, "a rejected membership removal must not survive")
	assert.Nil(t, members[0].EndsAt, "a rejected membership window must not survive")
}

// TestGroupPutMemberIsANoOpForAnUnchangedWindow pins that re-sending the same
// membership window neither rewrites the row nor advances the tenant policy
// revision, while still running the administrator guard that protects the
// last administrator.
func TestGroupPutMemberIsANoOpForAnUnchangedWindow(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix},
		guard, newOrganizationTestIDs(750),
	)
	service.now = func() time.Time { return organizationTestNow }
	created, err := service.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
	require.NoError(t, err)
	endsAt := organizationTestNow.Add(time.Hour)
	window := GroupMemberWindow{EndsAt: &endsAt}
	first, err := service.PutMember(t.Context(), 1, created.ID, 10, window)
	require.NoError(t, err)
	revision := h.revision(t, 1)

	repeated, err := service.PutMember(t.Context(), 1, created.ID, 10, window)
	require.NoError(t, err)
	assert.Equal(t, first, repeated)
	assert.Equal(t, revision, h.revision(t, 1), "an unchanged membership must not advance the policy revision")
	assert.Equal(t, 2, guard.verified, "the administrator guard still runs for an unchanged membership")
}
