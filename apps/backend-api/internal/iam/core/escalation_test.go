package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Every path that makes a principal hold a data-scoped permission through a
// role -- granting the permission, adding a member, binding the role to a
// group or entity, the indirect directory guard and rescoping the role --
// keeps the role's data scope within the actor's own constraint for it.
func TestRoleGrantabilityKeepsDataScopeWithinTheActorsConstraint(t *testing.T) {
	h := newCoreServiceHarness(t)
	tenantID, actorID, peerID := guid.ID(1), guid.ID(10), guid.ID(11)
	h.seedMember(t, tenantID, actorID, MemberActive)
	h.seedAdditionalMember(t, tenantID, peerID, MemberActive)
	now := repositoryTestTime
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, 101, NULL, 'Sales', 'sales', 'active', 0, 1, ?, ?),
		       (?, 102, 101, 'Sales east', 'sales east', 'active', 0, 1, ?, ?)`,
		tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_department_closure
		(tenant_id,ancestor_id,descendant_id,depth)
		VALUES (?,101,101,0),(?,101,102,1),(?,102,102,0)`, tenantID, tenantID, tenantID).Error)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_member_departments
		(tenant_id,principal_id,department_id,updated_at) VALUES (?, ?, 101, ?)`, tenantID, actorID, now).Error)
	role := func(name string, scope DataScope, departments []guid.ID, codes ...string) guid.ID {
		created, err := h.repository.CreateRole(t.Context(), tenantID, name, "")
		require.NoError(t, err)
		for _, code := range codes {
			_, err = h.repository.GrantPermission(t.Context(), tenantID, created.ID, code)
			require.NoError(t, err)
		}
		_, _, err = h.service.SetRoleDataScope(t.Context(), tenantID, created.ID, scope, departments)
		require.NoError(t, err)
		return created.ID
	}
	scopeOf := func(id guid.ID) DataScope {
		scope, err := h.service.GetRoleDataScope(t.Context(), tenantID, id)
		require.NoError(t, err)
		return scope.Scope
	}
	constraint := func() []string {
		c, err := h.authorizer.Constraint(t.Context(), tenantID, "todo_view", actorID)
		require.NoError(t, err)
		require.False(t, c.AllowAll)
		return c.DepartmentIDs
	}
	held := role("Sales tasks", DataScopeDepartment, nil, "todo_view", "role_create", "role_update", "role_grant_permission", "role_manage_member")
	_, err := h.repository.AddMember(t.Context(), tenantID, held, actorID)
	require.NoError(t, err)
	require.Equal(t, []string{"101"}, constraint(), "the actor reads todo rows of its own department only")
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID})

	// A new role has no scope row and so reaches all data.
	created, err := h.service.CreateRole(actor, tenantID, "New tasks", "")
	require.NoError(t, err)
	assert.Equal(t, DataScopeAll, scopeOf(created.ID))
	_, err = h.service.GrantPermission(actor, tenantID, created.ID, "todo_view")
	require.ErrorIs(t, err, ErrPrivilegeEscalation, "granting todo_view to an all-scope role")
	_, err = h.service.GrantPermission(actor, tenantID, created.ID, "role_create")
	require.NoError(t, err, "permissions without a data scope are unaffected")

	// Rescoping is bounded by the constraint, measured at the actor.
	for _, refused := range []RoleDataScope{
		{Scope: DataScopeAll},
		{Scope: DataScopeDepartmentAndDescendants},
		{Scope: DataScopeSelectedDepartments, DepartmentIDs: []guid.ID{102}},
		{Scope: DataScopeSelectedDepartments, DepartmentIDs: []guid.ID{101, 102}},
	} {
		_, _, err = h.service.SetRoleDataScope(actor, tenantID, held, refused.Scope, refused.DepartmentIDs)
		require.ErrorIs(t, err, ErrPrivilegeEscalation, "rescoping to %s %v", refused.Scope, refused.DepartmentIDs)
	}
	assert.Equal(t, DataScopeDepartment, scopeOf(held))
	for _, allowed := range []RoleDataScope{
		{Scope: DataScopeSelectedDepartments, DepartmentIDs: []guid.ID{101}},
		{Scope: DataScopeSelf},
		{Scope: DataScopeDepartment},
	} {
		_, _, err = h.service.SetRoleDataScope(actor, tenantID, created.ID, allowed.Scope, allowed.DepartmentIDs)
		require.NoError(t, err, "rescoping to %s %v", allowed.Scope, allowed.DepartmentIDs)
	}
	_, err = h.service.GrantPermission(actor, tenantID, created.ID, "todo_view")
	require.NoError(t, err, "a department-scoped role stays within the actor's department")
	_, err = h.service.AddMember(actor, tenantID, created.ID, peerID)
	require.NoError(t, err)

	// Every assignment path refuses a role that reaches beyond the actor.
	for _, wider := range []guid.ID{
		role("All tasks", DataScopeAll, nil, "todo_view"),
		role("Sales tree tasks", DataScopeDepartmentAndDescendants, nil, "todo_view"),
		role("East tasks", DataScopeSelectedDepartments, []guid.ID{102}, "todo_view"),
	} {
		_, err = h.service.AddMember(actor, tenantID, wider, peerID)
		assert.ErrorIs(t, err, ErrPrivilegeEscalation, "add member")
		_, err = h.service.AddDirectoryBinding(actor, tenantID, wider, DirectoryAssigneeGroup, 9001)
		assert.ErrorIs(t, err, ErrPrivilegeEscalation, "directory binding")
		_, _, err = h.service.PutEntityRoleBinding(actor, tenantID, 9002, wider, peerID, BindingAllow, time.Time{})
		assert.ErrorIs(t, err, ErrPrivilegeEscalation, "entity binding")
		guard := boundGuard(t, h.runtime, "sl_")
		guard.registry = h.service.registry
		assert.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			return guard.RequireGrantableRoles(actor, tx, tenantID, []guid.ID{wider})
		}), ErrPrivilegeEscalation, "group, invitation and access-request grants")
		members, err := h.repository.ListMembers(t.Context(), tenantID, wider)
		require.NoError(t, err)
		assert.Empty(t, members)
	}
	assert.Equal(t, []string{"101"}, constraint(), "authority is unchanged after the refusals")

	// A tenant administrator is unaffected.
	administrators := role("Administrators", DataScopeAll, nil, "tenant_administer")
	_, err = h.repository.AddMember(t.Context(), tenantID, administrators, actorID)
	require.NoError(t, err)
	_, _, err = h.service.SetRoleDataScope(actor, tenantID, held, DataScopeAll, nil)
	require.NoError(t, err)
	_, err = h.service.AddMember(actor, tenantID, held, peerID)
	require.NoError(t, err)
}

// An actor without a department cannot hand out a department scope: it would
// reach a department the actor cannot read at all.
func TestDepartmentScopeNeedsTheActorsOwnDepartment(t *testing.T) {
	h := newCoreServiceHarness(t)
	tenantID, actorID := guid.ID(1), guid.ID(10)
	h.seedMember(t, tenantID, actorID, MemberActive)
	created, err := h.repository.CreateRole(t.Context(), tenantID, "Department tasks", "")
	require.NoError(t, err)
	_, err = h.repository.GrantPermission(t.Context(), tenantID, created.ID, "todo_view")
	require.NoError(t, err)
	_, _, err = h.service.SetRoleDataScope(t.Context(), tenantID, created.ID, DataScopeDepartment, nil)
	require.NoError(t, err)
	_, err = h.repository.AddMember(t.Context(), tenantID, created.ID, actorID)
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID})
	_, err = h.service.AddMember(actor, tenantID, created.ID, actorID)
	assert.ErrorIs(t, err, ErrPrivilegeEscalation)
	_, _, err = h.service.SetRoleDataScope(actor, tenantID, created.ID, DataScopeSelf, nil)
	require.NoError(t, err, "narrowing to self stays available")
}

// Department and department-and-descendants scopes follow the member's
// department, so moving a member -- the actor included -- widens what those
// scopes reach without any grant being written. The move is bounded by the
// actor's own constraint for each permission the member holds that way.
func TestMemberDepartmentChangeStaysWithinTheActorsConstraint(t *testing.T) {
	h := newCoreServiceHarness(t)
	tenantID, actorID, peerID, selfOnlyID := guid.ID(1), guid.ID(10), guid.ID(11), guid.ID(12)
	h.seedMember(t, tenantID, actorID, MemberActive)
	h.seedAdditionalMember(t, tenantID, peerID, MemberActive)
	h.seedAdditionalMember(t, tenantID, selfOnlyID, MemberActive)
	now := repositoryTestTime
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, 101, NULL, 'Sales', 'sales', 'active', 0, 1, ?, ?),
		       (?, 102, 101, 'Sales east', 'sales east', 'active', 0, 1, ?, ?),
		       (?, 103, NULL, 'Support', 'support', 'active', 0, 1, ?, ?)`,
		tenantID, now, now, tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_department_closure
		(tenant_id,ancestor_id,descendant_id,depth)
		VALUES (?,101,101,0),(?,101,102,1),(?,102,102,0),(?,103,103,0)`, tenantID, tenantID, tenantID, tenantID).Error)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_member_departments
		(tenant_id,principal_id,department_id,updated_at) VALUES (?, ?, 101, ?)`, tenantID, actorID, now).Error)
	role := func(name string, scope DataScope, members ...guid.ID) guid.ID {
		created, err := h.repository.CreateRole(t.Context(), tenantID, name, "")
		require.NoError(t, err)
		_, err = h.repository.GrantPermission(t.Context(), tenantID, created.ID, "todo_view")
		require.NoError(t, err)
		_, _, err = h.service.SetRoleDataScope(t.Context(), tenantID, created.ID, scope, nil)
		require.NoError(t, err)
		for _, member := range members {
			_, err = h.repository.AddMember(t.Context(), tenantID, created.ID, member)
			require.NoError(t, err)
		}
		return created.ID
	}
	role("Department tasks", DataScopeDepartment, actorID, peerID)
	role("Own tasks", DataScopeSelf, selfOnlyID)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID})
	move := func(principalID, departmentID guid.ID) error {
		_, err := h.service.PutTenantMember(actor, tenantID, principalID, "Member", "", departmentID, MemberActive)
		return err
	}
	departmentOf := func(principalID guid.ID) guid.ID {
		member, err := h.repository.GetMember(t.Context(), tenantID, principalID)
		require.NoError(t, err)
		return member.DepartmentID
	}
	reach := func(principalID guid.ID) []string {
		constraint, err := h.authorizer.Constraint(t.Context(), tenantID, "todo_view", principalID)
		require.NoError(t, err)
		return constraint.DepartmentIDs
	}

	require.NoError(t, move(peerID, 101), "into the actor's own department")
	assert.Equal(t, []string{"101"}, reach(peerID))
	for _, outside := range []guid.ID{102, 103} {
		require.ErrorIs(t, move(peerID, outside), ErrPrivilegeEscalation, "into department %s", outside)
		assert.Equal(t, guid.ID(101), departmentOf(peerID), "a refused move changes nothing")
	}
	require.ErrorIs(t, move(actorID, 103), ErrPrivilegeEscalation, "the actor cannot move itself out of its constraint")
	assert.Equal(t, []string{"101"}, reach(actorID))
	require.NoError(t, move(peerID, 101), "keeping the department is not a change")
	require.NoError(t, move(peerID, 0), "clearing the department only narrows")
	assert.Empty(t, reach(peerID))
	require.NoError(t, move(selfOnlyID, 103), "a member without a department-based scope gains nothing")

	// A tenant administrator is unaffected.
	administrators, err := h.repository.CreateRole(t.Context(), tenantID, "Tenant administrators", "")
	require.NoError(t, err)
	_, err = h.repository.GrantPermission(t.Context(), tenantID, administrators.ID, "tenant_administer")
	require.NoError(t, err)
	_, err = h.repository.AddMember(t.Context(), tenantID, administrators.ID, actorID)
	require.NoError(t, err)
	require.NoError(t, move(peerID, 103))
	assert.Equal(t, []string{"103"}, reach(peerID))
}
