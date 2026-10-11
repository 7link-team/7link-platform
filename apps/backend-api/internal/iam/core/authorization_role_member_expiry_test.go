package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantUntil assigns a role with an end date and returns it.
func grantUntil(t *testing.T, h coreServiceHarness, tenantID, roleID, principalID guid.ID, end time.Time) time.Time {
	t.Helper()
	changed, err := h.service.AddMember(t.Context(), tenantID, roleID, principalID, &end)
	require.NoError(t, err)
	require.True(t, changed)
	return end
}

// roleWithPermission creates a role carrying one permission.
func roleWithPermission(t *testing.T, h coreServiceHarness, tenantID guid.ID, name, permission string) guid.ID {
	t.Helper()
	role, err := h.service.CreateRole(t.Context(), tenantID, name, "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), tenantID, role.ID, permission)
	require.NoError(t, err)
	return role.ID
}

func TestAnAssignmentStopsGrantingWhenItEnds(t *testing.T) {
	// The whole point of the column: the permission is there while the
	// assignment is, and gone afterwards, without anybody deleting a row.
	h := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	h.seedMember(t, tenant, principal, MemberActive)
	role := roleWithPermission(t, h, tenant, "Auditor", "role_view")
	end := grantUntil(t, h, tenant, role, principal, repositoryTestTime.Add(time.Hour))

	allowed, err := h.authorizer.Check(t.Context(), tenant, "role_view", principal)
	require.NoError(t, err)
	assert.True(t, allowed, "the assignment has not ended yet")

	*h.now = end.Add(-time.Millisecond)
	allowed, err = h.authorizer.Check(t.Context(), tenant, "role_view", principal)
	require.NoError(t, err)
	assert.True(t, allowed, "the last instant before the end still grants")

	*h.now = end
	allowed, err = h.authorizer.Check(t.Context(), tenant, "role_view", principal)
	require.NoError(t, err)
	assert.False(t, allowed, "the end is exclusive, as every other expiry here is")

	*h.now = end.Add(time.Hour)
	allowed, err = h.authorizer.Check(t.Context(), tenant, "role_view", principal)
	require.NoError(t, err)
	assert.False(t, allowed)

	// The row is still there. An assignment that lapsed is a thing that
	// happened, not a thing that never was, and removing it is a separate act.
	members, err := h.service.ListMembers(t.Context(), tenant, role)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{principal}, members)
	changed, err := h.service.RemoveMember(t.Context(), tenant, role, principal)
	require.NoError(t, err)
	assert.True(t, changed, "a lapsed assignment is still revocable")
}

func TestAnAssignmentWithNoEndIsWhatItAlwaysWas(t *testing.T) {
	// Every assignment written before the column existed reads as NULL, so the
	// absence of an end has to keep meaning "no end" however far the clock moves.
	h := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	h.seedMember(t, tenant, principal, MemberActive)
	role := roleWithPermission(t, h, tenant, "Auditor", "role_view")
	_, err := h.service.AddMember(t.Context(), tenant, role, principal, nil)
	require.NoError(t, err)

	for _, offset := range []time.Duration{0, time.Hour, 365 * 24 * time.Hour} {
		*h.now = repositoryTestTime.Add(offset)
		allowed, checkErr := h.authorizer.Check(t.Context(), tenant, "role_view", principal)
		require.NoError(t, checkErr)
		assert.True(t, allowed, "offset %s", offset)
	}
}

func TestGrantingIsAPutOfTheWholeAssignment(t *testing.T) {
	// The endpoint is a PUT, so the assignment ends up as the request described
	// it: an end date replaces whatever was there, and no end date removes one.
	// What must not change is the answer every existing caller already gets --
	// granting a role somebody already holds reports no change.
	h := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	h.seedMember(t, tenant, principal, MemberActive)
	role := roleWithPermission(t, h, tenant, "Auditor", "role_view")

	changed, err := h.service.AddMember(t.Context(), tenant, role, principal, nil)
	require.NoError(t, err)
	assert.True(t, changed, "the first grant is a change")
	changed, err = h.service.AddMember(t.Context(), tenant, role, principal, nil)
	require.NoError(t, err)
	assert.False(t, changed, "granting again changes nothing")

	end := repositoryTestTime.Add(time.Hour)
	changed, err = h.service.AddMember(t.Context(), tenant, role, principal, &end)
	require.NoError(t, err)
	assert.True(t, changed, "putting an end on a standing assignment is a change")
	changed, err = h.service.AddMember(t.Context(), tenant, role, principal, &end)
	require.NoError(t, err)
	assert.False(t, changed, "the same end again changes nothing")

	later := end.Add(time.Hour)
	changed, err = h.service.AddMember(t.Context(), tenant, role, principal, &later)
	require.NoError(t, err)
	assert.True(t, changed, "moving the end is a change")

	changed, err = h.service.AddMember(t.Context(), tenant, role, principal, nil)
	require.NoError(t, err)
	assert.True(t, changed, "dropping the end makes it standing again")
	*h.now = later.Add(time.Hour)
	allowed, err := h.authorizer.Check(t.Context(), tenant, "role_view", principal)
	require.NoError(t, err)
	assert.True(t, allowed, "and it grants past the end it used to have")
}

func TestTheRolesAMemberHoldsExcludeTheLapsedOnes(t *testing.T) {
	// ListMemberRoleIDs answers "what does this member hold", which is read as
	// authority: an administration screen offering a lapsed role would be
	// offering something that grants nothing.
	h := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	h.seedMember(t, tenant, principal, MemberActive)
	standing := roleWithPermission(t, h, tenant, "Standing", "role_view")
	ending := roleWithPermission(t, h, tenant, "Ending", "menu_view")
	_, err := h.service.AddMember(t.Context(), tenant, standing, principal, nil)
	require.NoError(t, err)
	end := grantUntil(t, h, tenant, ending, principal, repositoryTestTime.Add(time.Hour))

	held, err := h.repository.ListMemberRoleIDs(t.Context(), tenant, principal)
	require.NoError(t, err)
	assert.ElementsMatch(t, []guid.ID{standing, ending}, held)

	*h.now = end.Add(time.Minute)
	held, err = h.repository.ListMemberRoleIDs(t.Context(), tenant, principal)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{standing}, held, "only the standing assignment is still held")
}

func TestAnAssignmentThatEndsIsNotADurableAdministrator(t *testing.T) {
	// The guard asks whether the tenant would still have an administrator
	// afterwards. An administrator whose grant lapses next week is an answer that
	// expires, which is why this reads "has no end" rather than "has not ended"
	// -- the same thing the group branch has always required of a timed
	// membership.
	h := newCoreServiceHarness(t)
	tenant := guid.ID(1)
	standing, ending := guid.ID(11), guid.ID(22)
	h.seedMember(t, tenant, standing, MemberActive)
	h.seedAdditionalMember(t, tenant, ending, MemberActive)
	role := roleWithPermission(t, h, tenant, "Administrators", "tenant_administer")
	_, err := h.service.AddMember(t.Context(), tenant, role, standing, nil)
	require.NoError(t, err)
	grantUntil(t, h, tenant, role, ending, repositoryTestTime.Add(365*24*time.Hour))

	// Removing the only standing administrator is refused even though another
	// principal holds the role until next year.
	_, err = h.service.RemoveMember(t.Context(), tenant, role, standing)
	require.ErrorIs(t, err, ErrLastTenantAdministrator)

	// Removing the one whose grant ends is allowed: it was never the durable one.
	changed, err := h.service.RemoveMember(t.Context(), tenant, role, ending)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestACachedDecisionCannotOutliveTheAssignmentItCameFrom(t *testing.T) {
	// The snapshot machinery exists to bound a cached answer by its sources.
	// A permanent role used to have no bound; now it can, and the snapshot has
	// to carry it or the cache would keep granting after the assignment ended.
	h := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	h.seedMember(t, tenant, principal, MemberActive)
	role := roleWithPermission(t, h, tenant, "Auditor", "role_view")
	end := grantUntil(t, h, tenant, role, principal, repositoryTestTime.Add(time.Hour))

	snapshot, err := h.service.ListMemberAuthorizationSources(t.Context(), tenant, principal)
	require.NoError(t, err)
	var found bool
	for _, source := range snapshot.Sources {
		if source.Type != AuthorizationSourcePermanentRole || source.RoleID != role {
			continue
		}
		found = true
		require.NotNil(t, source.ExpiresAt, "the source names when it stops granting")
		assert.True(t, end.Equal(*source.ExpiresAt), "want %s, got %s", end, *source.ExpiresAt)
	}
	assert.True(t, found, "the assignment is among the snapshot's sources")
}

func TestASystemRoleAssignmentThatLapsedGrantsNothing(t *testing.T) {
	// The system path is a separate statement with no CTE, so it needs its own
	// proof: an expired system assignment must not keep platform authority alive.
	h := newCoreServiceHarness(t)
	principal := guid.ID(11)
	owner := seedSystemAccessFixture(t, h.db, "sl_", principal)
	_, err := h.repository.PutMember(t.Context(), TenantMember{
		TenantID: owner, PrincipalID: principal, DisplayName: "Operator", Status: MemberActive,
	})
	require.NoError(t, err)
	role := roleWithPermission(t, h, owner, SystemLegacyRolePrefix+"Operators", "platform_administer")
	end := grantUntil(t, h, owner, role, principal, repositoryTestTime.Add(time.Hour))

	allowed, err := h.authorizer.CheckPlatform(t.Context(), "platform_administer", principal)
	require.NoError(t, err)
	assert.True(t, allowed, "while the assignment stands")

	*h.now = end.Add(time.Minute)
	allowed, err = h.authorizer.CheckPlatform(t.Context(), "platform_administer", principal)
	require.NoError(t, err)
	assert.False(t, allowed, "and not after it ends")

	// Nor is it listed as a platform administrator any more.
	administrators, err := h.repository.ListPlatformAdministrators(t.Context())
	require.NoError(t, err)
	for _, administrator := range administrators {
		assert.NotEqual(t, principal, administrator.PrincipalID)
	}
}

func TestAGrantThatLapsedIsNotHeldAtThatMoment(t *testing.T) {
	// roleGrantedOn answers "is this role granted at this instant", which is what
	// a grant review and the escalation guard both ask. An assignment that has
	// lapsed is not held, in the same way an elapsed temporary grant is not.
	harness := newRepositoryHarness(t, "sl_")
	repository := seedRoleGrantedPrincipal(t, harness)
	role, err := repository.CreateRole(t.Context(), 1, "Held", "")
	require.NoError(t, err)
	end := repositoryTestTime.Add(time.Hour)
	_, err = repository.AddMember(t.Context(), 1, role.ID, 11, &end)
	require.NoError(t, err)

	granted, err := repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, repositoryTestTime)
	require.NoError(t, err)
	assert.True(t, granted)

	granted, err = repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, end)
	require.NoError(t, err)
	assert.False(t, granted, "the end is exclusive")

	granted, err = repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, end.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, granted)
}

func TestClosingAnAccountIsNotBlockedByAuthorityThatLapsed(t *testing.T) {
	// The guard refuses while a principal still holds system authority. An
	// assignment that has ended is not authority: it grants nothing in the
	// authorizer either, and leaving the account unclosable would make a
	// withdrawal depend on somebody deleting a row that no longer does anything.
	h := newCoreServiceHarness(t)
	principal := guid.ID(11)
	owner := seedSystemAccessFixture(t, h.db, "sl_", principal)
	_, err := h.repository.PutMember(t.Context(), TenantMember{
		TenantID: owner, PrincipalID: principal, DisplayName: "Operator", Status: MemberActive,
	})
	require.NoError(t, err)
	role := roleWithPermission(t, h, owner, "Operators", "account_view_create")
	end := repositoryTestTime.Add(time.Hour)
	_, err = h.repository.AddMember(t.Context(), owner, role, principal, &end)
	require.NoError(t, err)

	registry := h.service.registry
	require.ErrorIs(t,
		requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", principal, registry, repositoryTestTime),
		ErrPrincipalHasPlatformAccess, "while the assignment stands")
	require.NoError(t,
		requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", principal, registry, end.Add(time.Minute)),
		"and not after it ends")
}

func TestTheGrantListingSaysWhenEachAssignmentEnds(t *testing.T) {
	// The identifier-only listing beside this one cannot answer "until when", and
	// an administration screen has to show it or an end date is invisible once set.
	h := newCoreServiceHarness(t)
	tenant := guid.ID(1)
	standing, ending := guid.ID(11), guid.ID(22)
	h.seedMember(t, tenant, standing, MemberActive)
	h.seedAdditionalMember(t, tenant, ending, MemberActive)
	role := roleWithPermission(t, h, tenant, "Auditors", "role_view")
	_, err := h.service.AddMember(t.Context(), tenant, role, standing, nil)
	require.NoError(t, err)
	end := grantUntil(t, h, tenant, role, ending, repositoryTestTime.Add(time.Hour))

	grants, total, err := h.service.ListRoleMemberGrantsPage(t.Context(), tenant, role, 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, grants, 2)
	ends := map[guid.ID]*time.Time{}
	for _, grant := range grants {
		ends[grant.PrincipalID] = grant.ExpiresAt
	}
	assert.Nil(t, ends[standing], "a standing assignment reports no end")
	require.NotNil(t, ends[ending])
	assert.True(t, end.Equal(*ends[ending]), "want %s, got %s", end, *ends[ending])

	// A lapsed assignment is still listed: the row is there, removing it is still
	// an act, and a screen that hid it would offer to grant a role whose write
	// then reports no change.
	*h.now = end.Add(time.Hour)
	grants, total, err = h.service.ListRoleMemberGrantsPage(t.Context(), tenant, role, 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, grants, 2)

	_, _, err = h.service.ListRoleMemberGrantsPage(t.Context(), 0, role, 0, 50)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = h.service.ListRoleMemberGrantsPage(t.Context(), tenant, guid.ID(999999), 0, 50)
	assert.ErrorIs(t, err, ErrRoleNotFound)
}
