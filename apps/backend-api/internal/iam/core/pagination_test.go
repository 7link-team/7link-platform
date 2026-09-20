package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A role's membership grows with the tenant, so the listing has to be bounded
// by what the caller asked for rather than by what the table happens to hold.
// Each assertion here fails on its own if one half of that stops working: the
// limit, the offset, the unfiltered total, or the role check that has to run
// before any of them.
func TestListRoleMembersPageBoundsTheResult(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	principals := []guid.ID{11, 22, 33, 44, 55}
	// seedTenantAndPrincipal inserts the tenant as well, so only the first call
	// may carry it; the rest would collide on the tenant's primary key.
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, principals[0])
	for _, principal := range principals[1:] {
		seedHTTPPrincipal(t, harness.repositoryHarness, principal)
	}
	for _, principal := range principals {
		_, err := repository.PutMember(t.Context(), TenantMember{
			TenantID: 1, PrincipalID: principal, DisplayName: "Member", Email: principal.String() + "@example.test", Status: MemberActive,
		})
		require.NoError(t, err)
	}
	role, err := repository.CreateRole(t.Context(), 1, "Operators", "")
	require.NoError(t, err)
	for _, principal := range principals {
		_, err = repository.AddMember(t.Context(), 1, role.ID, principal)
		require.NoError(t, err)
	}

	first, total, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(principals)), total, "the total counts every holder, not the page")

	second, total, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.NotEqual(t, first, second, "the offset must move the window")
	assert.Empty(t, intersect(first, second), "pages must not overlap")

	last, _, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to come from somewhere, and the transport is
	// where a caller can be held to it.
	all, _, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 0)
	require.NoError(t, err)
	assert.Len(t, all, len(principals))

	// An unknown role is not an empty membership. Reporting it as one would let a
	// caller conclude a role exists and has nobody in it.
	_, _, err = repository.ListRoleMembersPage(t.Context(), 1, role.ID+9999, 0, 2)
	assert.Error(t, err)

	_, _, err = harness.service.ListRoleMembersPage(t.Context(), 0, role.ID, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	page, total, err := harness.service.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 3)
	require.NoError(t, err)
	assert.Len(t, page, 3)
	assert.Equal(t, int64(len(principals)), total)
}

// Bindings on one entity grow with the tenant's principals and roles, so the
// listing has to be bounded by the request rather than by the table. Each
// assertion fails on its own if the limit, the offset, the unfiltered total or
// the entity check that has to precede them stops working.
func TestListEntityRoleBindingsPageBoundsTheResult(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	entity, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "Store", Status: EntityActive,
	})
	require.NoError(t, err)
	role, err := repository.CreateRole(t.Context(), 1, "Operators", "")
	require.NoError(t, err)
	principals := []guid.ID{11, 22, 33, 44, 55}
	for _, principal := range principals {
		_, _, err = repository.PutEntityRoleBinding(
			t.Context(), 1, entity.ID, role.ID, principal, BindingAllow, time.Time{},
		)
		require.NoError(t, err)
	}

	first, total, err := repository.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(principals)), total, "the total counts every binding, not the page")

	second, total, err := repository.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.Empty(t, intersect(bindingPrincipals(first), bindingPrincipals(second)),
		"the offset must move the window and the pages must not overlap")

	last, _, err := repository.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	all, _, err := repository.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID, 0, 0)
	require.NoError(t, err)
	assert.Len(t, all, len(principals), "zero means unbounded here; the HTTP input is where the bound is enforced")

	// An unknown entity is not an empty binding set.
	_, _, err = repository.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID+9999, 0, 2)
	assert.Error(t, err)

	_, _, err = harness.service.ListEntityRoleBindingsPage(t.Context(), 0, entity.ID, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	page, total, err := harness.service.ListEntityRoleBindingsPage(t.Context(), 1, entity.ID, 0, 3)
	require.NoError(t, err)
	assert.Len(t, page, 3)
	assert.Equal(t, int64(len(principals)), total)
}

func bindingPrincipals(bindings []EntityRoleBinding) []guid.ID {
	ids := make([]guid.ID, len(bindings))
	for index, binding := range bindings {
		ids[index] = binding.PrincipalID
	}
	return ids
}

// Relationship grants grow with the tenant, and the listing reads one of two
// tables depending on the filter, so both arms have to be bounded.
func TestListRelationshipsPageBoundsBothArms(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	owner, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "Owner", Status: EntityActive,
	})
	require.NoError(t, err)
	subjects := []guid.ID{11, 22, 33, 44, 55}
	for _, subject := range subjects {
		_, _, err = repository.PutRelationship(t.Context(), Relationship{
			TenantID: 1, SubjectType: "principal", SubjectID: subject,
			Relation: "viewer", ResourceType: "store", ResourceID: owner.ID,
			CreatedAt: repositoryTestTime,
		})
		require.NoError(t, err)
		_, _, err = repository.PutRelationship(t.Context(), Relationship{
			TenantID: 1, EntityID: owner.ID, SubjectType: "principal", SubjectID: subject,
			Relation: "editor", ResourceType: "document", ResourceID: 700,
			CreatedAt: repositoryTestTime,
		})
		require.NoError(t, err)
	}

	for name, filter := range map[string]RelationshipFilter{
		"tenant entity grants": {},
		"business resources":   {EntityID: owner.ID},
	} {
		t.Run(name, func(t *testing.T) {
			first, total, err := repository.ListRelationshipsPage(t.Context(), 1, filter, 0, 2)
			require.NoError(t, err)
			assert.Len(t, first, 2, "the limit must bound the page")
			assert.Equal(t, int64(len(subjects)), total, "the total counts every match, not the page")

			second, total, err := repository.ListRelationshipsPage(t.Context(), 1, filter, 2, 2)
			require.NoError(t, err)
			assert.Len(t, second, 2)
			assert.Equal(t, int64(len(subjects)), total)
			assert.Empty(t, intersect(relationshipSubjects(first), relationshipSubjects(second)),
				"the offset must move the window and the pages must not overlap")

			last, _, err := repository.ListRelationshipsPage(t.Context(), 1, filter, 4, 2)
			require.NoError(t, err)
			assert.Len(t, last, 1, "a final short page returns what is left, not an error")

			all, _, err := repository.ListRelationshipsPage(t.Context(), 1, filter, 0, 0)
			require.NoError(t, err)
			assert.Len(t, all, len(subjects))
		})
	}

	// The narrowing filters still page, and they still count only what they match.
	narrowed, total, err := repository.ListRelationshipsPage(
		t.Context(), 1, RelationshipFilter{ResourceType: "store", ResourceID: owner.ID}, 0, 2,
	)
	require.NoError(t, err)
	assert.Len(t, narrowed, 2)
	assert.Equal(t, int64(len(subjects)), total)
	narrowed, total, err = repository.ListRelationshipsPage(
		t.Context(), 1,
		RelationshipFilter{EntityID: owner.ID, ResourceType: "document", ResourceID: 700}, 0, 2,
	)
	require.NoError(t, err)
	assert.Len(t, narrowed, 2)
	assert.Equal(t, int64(len(subjects)), total)

	_, _, err = harness.service.ListRelationshipsPage(t.Context(), 0, RelationshipFilter{}, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = harness.service.ListRelationshipsPage(
		t.Context(), 1, RelationshipFilter{ResourceType: "NOT A TYPE"}, 0, 2,
	)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	page, total, err := harness.service.ListRelationshipsPage(t.Context(), 1, RelationshipFilter{}, 0, 3)
	require.NoError(t, err)
	assert.Len(t, page, 3)
	assert.Equal(t, int64(len(subjects)), total)
}

func relationshipSubjects(relationships []Relationship) []guid.ID {
	ids := make([]guid.ID, len(relationships))
	for index, relationship := range relationships {
		ids[index] = relationship.SubjectID
	}
	return ids
}

// Directory assignments live in two tables and are returned as one ordered
// listing, so the window has to walk the groups and then continue into the
// positions. The page that straddles the boundary is the one that proves it.
func TestListDirectoryBindingsPageWindowsAcrossBothTables(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	groups := []guid.ID{200, 201, 202}
	positions := []guid.ID{300, 301, 302}
	seedDirectoryAssignees(t, harness.repositoryHarness, 1, groups, positions)
	role, err := repository.CreateRole(t.Context(), 1, "Operators", "")
	require.NoError(t, err)
	for _, group := range groups {
		_, err = repository.ChangeDirectoryBinding(t.Context(), 1, role.ID, DirectoryAssigneeGroup, group, true)
		require.NoError(t, err)
	}
	for _, position := range positions {
		_, err = repository.ChangeDirectoryBinding(t.Context(), 1, role.ID, DirectoryAssigneePosition, position, true)
		require.NoError(t, err)
	}
	total := int64(len(groups) + len(positions))

	first, reported, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 0, 2)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{200, 201}, bindingAssignees(first), "the limit must bound the page")
	assert.Equal(t, total, reported, "the total counts both tables, not the page")

	straddling, reported, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{202, 300}, bindingAssignees(straddling),
		"a page that runs out of groups continues into the positions")
	assert.Equal(t, total, reported)
	assert.Equal(t, DirectoryAssigneeGroup, straddling[0].AssigneeType)
	assert.Equal(t, DirectoryAssigneePosition, straddling[1].AssigneeType)

	tail, _, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 4, 2)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{301, 302}, bindingAssignees(tail),
		"an offset past the groups indexes into the positions alone")

	aligned, _, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 3, 2)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{300, 301}, bindingAssignees(aligned),
		"an offset exactly at the boundary starts at the first position")

	past, _, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 6, 2)
	require.NoError(t, err)
	assert.Empty(t, past, "an offset past the end is an empty page, not an error")

	all, _, err := repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{200, 201, 202, 300, 301, 302}, bindingAssignees(all))

	_, _, err = repository.ListDirectoryBindingsPage(t.Context(), 1, role.ID+9999, 0, 2)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	_, _, err = harness.service.ListDirectoryBindingsPage(t.Context(), 0, role.ID, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	page, reported, err := harness.service.ListDirectoryBindingsPage(t.Context(), 1, role.ID, 0, 4)
	require.NoError(t, err)
	assert.Len(t, page, 4)
	assert.Equal(t, total, reported)
}

func bindingAssignees(bindings []RoleDirectoryBinding) []guid.ID {
	ids := make([]guid.ID, len(bindings))
	for index, binding := range bindings {
		ids[index] = binding.AssigneeID
	}
	return ids
}

func seedDirectoryAssignees(
	t *testing.T,
	harness repositoryHarness,
	tenantID guid.ID,
	groups, positions []guid.ID,
) {
	t.Helper()
	now := repositoryTestTime
	for _, group := range groups {
		require.NoError(t, harness.db.Exec(
			"INSERT INTO sl_iam_groups (tenant_id, id, name, name_key, group_type, rule_json, description, status, sort_order, version, created_at, updated_at) VALUES (?, ?, ?, ?, 'static', '', '', 'active', 0, 1, ?, ?)",
			tenantID, group, "Group "+group.String(), "group-"+group.String(), now, now,
		).Error)
	}
	for _, position := range positions {
		require.NoError(t, harness.db.Exec(
			"INSERT INTO sl_iam_positions (tenant_id, id, code, name, status, sort_order, version, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', 0, 1, ?, ?)",
			tenantID, position, "position-"+position.String(), "Position "+position.String(), now, now,
		).Error)
	}
}

// A member's roles are the union of their permanent memberships and their active
// temporary grants, so the window is taken after the two are merged. The page
// still has to be bounded and the total still has to describe the whole union.
func TestListTenantMemberRolesPageBoundsTheUnion(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	harness.seedMember(t, 1, 11, MemberActive)
	roles := make([]guid.ID, 0, 5)
	for index := range 5 {
		role, err := repository.CreateRole(t.Context(), 1, "Role "+guid.ID(index).String(), "")
		require.NoError(t, err)
		_, err = repository.AddMember(t.Context(), 1, role.ID, 11)
		require.NoError(t, err)
		roles = append(roles, role.ID)
	}

	first, total, err := harness.service.ListTenantMemberRolesPage(t.Context(), 1, 11, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(roles)), total, "the total counts the whole union, not the page")

	second, total, err := harness.service.ListTenantMemberRolesPage(t.Context(), 1, 11, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(roles)), total)
	assert.Empty(t, intersect(first, second), "the offset must move the window; the pages must not overlap")

	last, _, err := harness.service.ListTenantMemberRolesPage(t.Context(), 1, 11, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	past, total, err := harness.service.ListTenantMemberRolesPage(t.Context(), 1, 11, 99, 2)
	require.NoError(t, err)
	assert.Empty(t, past, "an offset past the end is an empty page, not an error")
	assert.Equal(t, int64(len(roles)), total, "and it still reports the whole union")

	all, _, err := harness.service.ListTenantMemberRolesPage(t.Context(), 1, 11, 0, 0)
	require.NoError(t, err)
	assert.Len(t, all, len(roles))

	_, _, err = harness.service.ListTenantMemberRolesPage(t.Context(), 1, 999, 0, 2)
	assert.Error(t, err, "an unknown member is not an empty role set")
}

// Platform administrators are assembled per principal from two tables, so the
// window is taken after the grouping that decides how many there are.
func TestListPlatformAdministratorsPageBoundsTheResult(t *testing.T) {
	harness := newCoreServiceHarness(t)
	principals := []guid.ID{11, 22, 33, 44, 55}
	for _, principal := range principals {
		_, err := harness.repository.SetPlatformAdministrator(
			t.Context(), principal, false, []string{"platform_read", "platform_write"},
		)
		require.NoError(t, err)
	}

	first, total, err := harness.service.ListPlatformAdministratorsPage(t.Context(), 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(principals)), total,
		"the total counts administrators after grouping, not the rows behind them")

	second, total, err := harness.service.ListPlatformAdministratorsPage(t.Context(), 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.Empty(t, intersect(administratorPrincipals(first), administratorPrincipals(second)),
		"the offset must move the window; the pages must not overlap")

	last, _, err := harness.service.ListPlatformAdministratorsPage(t.Context(), 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	all, _, err := harness.service.ListPlatformAdministratorsPage(t.Context(), 0, 0)
	require.NoError(t, err)
	assert.Len(t, all, len(principals))
}

func administratorPrincipals(administrators []PlatformAdministrator) []guid.ID {
	ids := make([]guid.ID, len(administrators))
	for index, administrator := range administrators {
		ids[index] = administrator.PrincipalID
	}
	return ids
}

// The field policy listing is a projection of the declared catalog onto the
// tenant's stored rows, so it exists only in memory -- and it still has to be
// bounded before it reaches the caller.
func TestListFieldPoliciesPageBoundsTheResult(t *testing.T) {
	harness := newCoreServiceHarness(t)
	harness.seedMember(t, 1, 11, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), 1, "Policy readers", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), 1, role.ID, "field_policy_view")
	require.NoError(t, err)
	_, err = harness.repository.AddMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})

	all, total, err := harness.service.ListFieldPoliciesPage(ctx, 1, "", 0, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 3, "the catalog must be long enough for a page to be observable")
	assert.Equal(t, int64(len(all)), total)

	first, total, err := harness.service.ListFieldPoliciesPage(ctx, 1, "", 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(all)), total, "the total counts the whole catalog projection, not the page")
	assert.Equal(t, all[:2], first)

	second, total, err := harness.service.ListFieldPoliciesPage(ctx, 1, "", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, all[2:min(4, len(all))], second, "the offset must move the window")
	assert.Equal(t, int64(len(all)), total)

	past, total, err := harness.service.ListFieldPoliciesPage(ctx, 1, "", len(all)+10, 2)
	require.NoError(t, err)
	assert.Empty(t, past, "an offset past the end is an empty page, not an error")
	assert.Equal(t, int64(len(all)), total)

	_, _, err = harness.service.ListFieldPoliciesPage(t.Context(), 1, "", 0, 2)
	assert.ErrorIs(t, err, ErrPrivilegeEscalation, "paging must not bypass the access check")
}

// Every one of these listings must be bounded when the caller sends nothing at
// all: that is the whole point, and it is decided by the HTTP input's default
// rather than by anything the repository does. A handler that still answered
// with OK instead of List would report no page here.
func TestListEndpointsBoundTheDefaultPage(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	tenant := tenantPathPrefix(1)
	entity, err := harness.repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "Store", Status: EntityActive,
	})
	require.NoError(t, err)
	role, err := harness.repository.CreateRole(t.Context(), 1, "Operators", "")
	require.NoError(t, err)

	for _, route := range []string{
		"/entities",
		"/roles",
		"/relationships",
		"/field-policies",
		"/platform/administrators",
		"/members/11/roles",
		"/entities/" + entity.ID.String() + "/role-bindings",
		"/roles/" + role.ID.String() + "/directory-bindings",
	} {
		t.Run(route, func(t *testing.T) {
			path := route
			if !strings.HasPrefix(route, "/platform/") {
				path = tenant + route
			}
			response := harness.api.Get(path, "Authorization: Bearer test")
			require.Equal(t, 200, response.Code, response.Body.String())
			page := decodeEnvelope[json.RawMessage](t, response).Meta.Page
			require.NotNil(t, page, "a list response has to carry its page")
			assert.Equal(t, 50, page.Limit, "omitting limit must mean one bounded page, not the whole table")
			assert.Equal(t, 0, page.Offset)
		})
	}
}

func intersect(left, right []guid.ID) []guid.ID {
	seen := make(map[guid.ID]bool, len(left))
	for _, id := range left {
		seen[id] = true
	}
	var both []guid.ID
	for _, id := range right {
		if seen[id] {
			both = append(both, id)
		}
	}
	return both
}
