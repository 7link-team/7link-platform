package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type coreServiceHarness struct {
	repositoryHarness
	service    *Service
	authorizer *Authorizer
	events     *[]audit.DomainEvent
}

type policyPutResult struct {
	changed bool
	err     error
}

func newCoreServiceHarness(t *testing.T) coreServiceHarness {
	t.Helper()
	harness := newRepositoryHarness(t, "sl_")
	authorizer := NewAuthorizer(harness.runtime)
	authorizer.now = harness.repository.now
	events := make([]audit.DomainEvent, 0)
	appender := func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		events = append(events, event)
		return nil
	}
	return coreServiceHarness{
		repositoryHarness: harness,
		service:           NewService(authz.DefaultRegistry(), harness.repository, authorizer, appender),
		authorizer:        authorizer,
		events:            &events,
	}
}

func (h coreServiceHarness) seedMember(t *testing.T, tenantID, principalID guid.ID, status MemberStatus) TenantMember {
	t.Helper()
	seedTenantAndPrincipal(t, h.db, "sl_", tenantID, principalID)
	member, err := h.service.PutTenantMember(
		t.Context(), tenantID, principalID, "Member "+principalID.String(),
		principalID.String()+"@example.test", 0, status,
	)
	require.NoError(t, err)
	return member
}

func (h coreServiceHarness) seedAdditionalMember(
	t *testing.T,
	tenantID, principalID guid.ID,
	status MemberStatus,
) TenantMember {
	t.Helper()
	require.NoError(t, h.db.Exec(
		"INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)",
		principalID, "principal-"+principalID.String(), "Principal "+principalID.String(),
		repositoryTestTime, repositoryTestTime,
	).Error)
	member, err := h.service.PutTenantMember(
		t.Context(), tenantID, principalID, "Member "+principalID.String(), "", 0, status,
	)
	require.NoError(t, err)
	return member
}

func TestServiceRoleMemberAndPermissionLifecycle(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)

	role, err := harness.service.CreateRole(t.Context(), tenantID, " Operators ", "Can inspect roles")
	require.NoError(t, err)
	assert.Equal(t, "Operators", role.Name)

	changed, err := harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	assert.False(t, changed)

	changed, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)
	assert.True(t, changed)
	allowed, err := harness.authorizer.Check(t.Context(), tenantID, "role_view", principalID)
	require.NoError(t, err)
	assert.True(t, allowed)

	permissions, err := harness.service.ListPermissions(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"role_view"}, permissions)
	grants, err := harness.service.ListPermissionGrants(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "role_view", grants[0].PermissionCode)
	members, err := harness.service.ListMembers(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{principalID}, members)

	name, description := "Reviewers", "Updated"
	updated, err := harness.service.UpdateRole(t.Context(), tenantID, role.ID, &name, &description)
	require.NoError(t, err)
	assert.Equal(t, name, updated.Name)
	assert.Equal(t, description, updated.Description)

	changed, err = harness.service.RemoveMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)
	assert.True(t, changed)
	allowed, err = harness.authorizer.Check(t.Context(), tenantID, "role_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed)

	changed, err = harness.service.RevokePermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	assert.True(t, changed)
	require.NoError(t, harness.service.DeleteRole(t.Context(), tenantID, role.ID))
	_, err = harness.service.GetRole(t.Context(), tenantID, role.ID)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	assert.NotEmpty(t, *harness.events)
}

func TestServiceRejectsPrivilegeEscalationFromAuthenticatedActor(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, actorID, targetID := guid.ID(1), guid.ID(10), guid.ID(11)
	seedTenantAndPrincipal(t, harness.db, "sl_", tenantID, actorID)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_tenant_members (tenant_id, principal_id, display_name, email, status, created_at, updated_at) VALUES (?, ?, ?, '', 'active', ?, ?)",
		tenantID, actorID, "Actor", repositoryTestTime, repositoryTestTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)",
		targetID, "target", "Target", repositoryTestTime, repositoryTestTime,
	).Error)
	_, err := harness.service.PutTenantMember(t.Context(), tenantID, targetID, "Target", "", 0, MemberActive)
	require.NoError(t, err)

	role, err := harness.service.CreateRole(t.Context(), tenantID, "Privileged", "")
	require.NoError(t, err)
	claims := &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID}
	actorContext := authn.WithClaims(t.Context(), claims)
	_, err = harness.service.GrantPermission(actorContext, tenantID, role.ID, "role_view")
	assert.ErrorIs(t, err, ErrPrivilegeEscalation)

	_, err = harness.repository.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(actorContext, tenantID, role.ID, targetID)
	assert.ErrorIs(t, err, ErrPrivilegeEscalation)

	actorRole, err := harness.repository.CreateRole(t.Context(), tenantID, "Actor permissions", "")
	require.NoError(t, err)
	_, err = harness.repository.GrantPermission(t.Context(), tenantID, actorRole.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.repository.AddMember(t.Context(), tenantID, actorRole.ID, actorID)
	require.NoError(t, err)
	changed, err := harness.service.AddMember(actorContext, tenantID, role.ID, targetID)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestDeletingEntityDenyRequiresGrantableRole(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, actorID, targetID := guid.ID(1), guid.ID(10), guid.ID(11)
	harness.seedMember(t, tenantID, actorID, MemberActive)
	harness.seedAdditionalMember(t, tenantID, targetID, MemberActive)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	deniedRole, err := harness.service.CreateRole(t.Context(), tenantID, "Denied privilege", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, deniedRole.ID, "user_delete")
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, entity.ID, deniedRole.ID, targetID, BindingDeny, time.Time{},
	)
	require.NoError(t, err)

	actorContext := authn.WithClaims(t.Context(), &authn.Claims{
		SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID,
	})
	_, err = harness.service.DeleteEntityRoleBinding(
		actorContext, tenantID, entity.ID, deniedRole.ID, targetID,
	)
	assert.ErrorIs(t, err, ErrPrivilegeEscalation)
	bindings, err := harness.service.ListEntityRoleBindings(t.Context(), tenantID, entity.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1, "failed anti-escalation check must preserve the deny")

	actorRole, err := harness.repository.CreateRole(t.Context(), tenantID, "Actor privileges", "")
	require.NoError(t, err)
	_, err = harness.repository.GrantPermission(t.Context(), tenantID, actorRole.ID, "user_delete")
	require.NoError(t, err)
	_, err = harness.repository.AddMember(t.Context(), tenantID, actorRole.ID, actorID)
	require.NoError(t, err)
	changed, err := harness.service.DeleteEntityRoleBinding(
		actorContext, tenantID, entity.ID, deniedRole.ID, targetID,
	)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestServiceMenuEntityAndAuthorizationLifecycle(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Store viewers", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)

	root, err := harness.service.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, Label: "Administration", Status: MenuActive,
	})
	require.NoError(t, err)
	child, err := harness.service.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, ParentID: root.ID, Label: "Stores", Route: "/stores",
		PermissionCode: "store_view", Status: MenuActive,
	})
	require.NoError(t, err)
	effective, err := harness.service.EffectiveMenus(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	require.Len(t, effective, 1)
	require.Len(t, effective[0].Children, 1)
	assert.Equal(t, child.ID.String(), effective[0].Children[0].ID)

	root.ParentID = child.ID
	_, err = harness.service.UpdateMenu(t.Context(), root)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	assert.ErrorIs(t, harness.service.DeleteMenu(t.Context(), tenantID, root.ID), ErrMenuHasChildren)

	company, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "company", Name: "Company",
	})
	require.NoError(t, err)
	store, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, ParentID: company.ID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	binding, changed, err := harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, company.ID, role.ID, principalID, BindingAllow, time.Time{},
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, company.ID, binding.EntityID)
	allowed, err := harness.authorizer.CheckEntity(t.Context(), tenantID, store.ID, "store_view", principalID)
	require.NoError(t, err)
	assert.True(t, allowed, "an entity-scoped grant must reach active descendants")

	parent := store.ID
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, company.ID, EntityPatch{ParentID: &parent})
	assert.ErrorIs(t, err, ErrEntityHierarchy)
	assert.ErrorIs(t, harness.service.DeleteEntity(t.Context(), tenantID, company.ID), ErrEntityHasChildren)

	changed, err = harness.service.DeleteEntityRoleBinding(t.Context(), tenantID, company.ID, role.ID, principalID)
	require.NoError(t, err)
	assert.True(t, changed)
	allowed, err = harness.authorizer.CheckEntity(t.Context(), tenantID, store.ID, "store_view", principalID)
	require.NoError(t, err)
	assert.True(t, allowed, "the tenant-wide role membership remains effective")
}

func TestServiceDataScopeDirectoryBindingAndPlatformAdministration(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Scoped", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "user_view")
	require.NoError(t, err)

	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, ?, NULL, 'Engineering', 'engineering', 'active', 0, 1, ?, ?)`,
		tenantID, guid.ID(101), now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (?, ?, 'Operators', 'operators', 'static', '', '', 'active', 0, 1, ?, ?)`,
		tenantID, guid.ID(201), now, now).Error)

	scope, changed, err := harness.service.SetRoleDataScope(
		t.Context(), tenantID, role.ID, DataScopeSelectedDepartments, []guid.ID{101, 101},
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []guid.ID{101}, scope.DepartmentIDs)
	_, changed, err = harness.service.SetRoleDataScope(
		t.Context(), tenantID, role.ID, DataScopeSelectedDepartments, []guid.ID{101},
	)
	require.NoError(t, err)
	assert.False(t, changed)

	changed, err = harness.service.AddDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneeGroup, guid.ID(201),
	)
	require.NoError(t, err)
	assert.True(t, changed)
	bindings, err := harness.service.ListDirectoryBindings(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.Equal(t, guid.ID(201), bindings[0].AssigneeID)
	changed, err = harness.service.RemoveDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneeGroup, guid.ID(201),
	)
	require.NoError(t, err)
	assert.True(t, changed)

	platformPrincipal := guid.ID(21)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, 'platform', 'Platform', 'active', ?, ?)",
		platformPrincipal, now, now,
	).Error)
	admin, err := harness.service.SetPlatformAdministrator(t.Context(), platformPrincipal, true, nil)
	require.NoError(t, err)
	assert.True(t, admin.FullAdministrator)
	allowed, err := harness.authorizer.CheckPlatform(t.Context(), "platform_administer", platformPrincipal)
	require.NoError(t, err)
	assert.True(t, allowed)
	_, err = harness.service.DeletePlatformAdministrator(t.Context(), platformPrincipal)
	assert.ErrorIs(t, err, ErrLastPlatformAdministrator)

	catalog := harness.service.PlatformPermissionCatalog(t.Context())
	require.NotEmpty(t, catalog)
	codes := make([]string, len(catalog))
	for index := range catalog {
		codes[index] = catalog[index].Code
		assert.Equal(t, "platform", catalog[index].Scope)
	}
	assert.True(t, sort.StringsAreSorted(codes))
}

func TestServiceValidationAndTenantIsolation(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantA, tenantB := guid.ID(1), guid.ID(2)
	principalA, principalB := guid.ID(11), guid.ID(22)
	harness.seedMember(t, tenantA, principalA, MemberActive)
	harness.seedMember(t, tenantB, principalB, MemberActive)
	roleB, err := harness.service.CreateRole(t.Context(), tenantB, "Tenant B", "")
	require.NoError(t, err)

	_, err = harness.service.GetRole(t.Context(), tenantA, roleB.ID)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	_, err = harness.service.AddMember(t.Context(), tenantA, roleB.ID, principalA)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	_, err = harness.service.CreateRole(t.Context(), 0, "invalid", "")
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.CreateRole(t.Context(), tenantA, "", "")
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.UpdateRole(t.Context(), tenantA, guid.ID(999), nil, nil)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.GrantPermission(t.Context(), tenantA, guid.ID(999), "missing")
	assert.True(t, errors.Is(err, ErrPermissionNotFound) || errors.Is(err, ErrRoleNotFound))
	_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantA, guid.ID(999), DataScope("bad"), nil)
	assert.ErrorIs(t, err, ErrInvalidRoleDataScope)
	_, err = harness.service.AddDirectoryBinding(t.Context(), tenantA, guid.ID(999), DirectoryAssigneeType("bad"), 1)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestServiceMemberLifecycleFiltersAndMembershipChecker(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, aliceID, bobID := guid.ID(1), guid.ID(11), guid.ID(12)
	alice := harness.seedMember(t, tenantID, aliceID, MemberActive)
	assert.Equal(t, "Member 11", alice.DisplayName)
	harness.seedAdditionalMember(t, tenantID, bobID, MemberDisabled)

	stored, err := harness.service.GetTenantMember(t.Context(), tenantID, aliceID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, stored.PrincipalID)
	members, total, err := harness.service.ListTenantMembers(t.Context(), tenantID, MemberFilter{
		Search: "member 11", Status: MemberActive, Limit: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, members, 1)
	assert.Equal(t, aliceID, members[0].PrincipalID)

	checker := NewMembershipChecker(harness.runtime)
	active, err := checker.IsMemberActive(t.Context(), tenantID, aliceID)
	require.NoError(t, err)
	assert.True(t, active)
	active, err = checker.IsMemberActiveOn(t.Context(), harness.db, tenantID, bobID)
	require.NoError(t, err)
	assert.False(t, active)

	disabled, err := harness.service.SetTenantMemberStatus(t.Context(), tenantID, aliceID, MemberDisabled)
	require.NoError(t, err)
	assert.False(t, disabled.DisabledAt.IsZero())
	active, err = checker.IsMemberActive(t.Context(), tenantID, aliceID)
	require.NoError(t, err)
	assert.False(t, active)
	_, err = harness.service.SetTenantMemberStatus(t.Context(), tenantID, 999, MemberActive)
	assert.ErrorIs(t, err, ErrMemberNotFound)
	assert.Panics(t, func() { NewMembershipChecker(nil) })
}

func TestServiceMemberDepartmentAndInputValidation(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	seedTenantAndPrincipal(t, harness.db, "sl_", tenantID, principalID)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, 101, NULL, 'Active', 'active', 'active', 0, 1, ?, ?),
		       (?, 102, NULL, 'Disabled', 'disabled', 'disabled', 0, 1, ?, ?)`,
		tenantID, now, now, tenantID, now, now).Error)

	member, err := harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, " User ", "user@example.test", 101, MemberActive,
	)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(101), member.DepartmentID)
	_, err = harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, "User", "", 999, MemberActive,
	)
	assert.ErrorIs(t, err, ErrMemberDepartmentMissing)
	_, err = harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, "User", "", 102, MemberActive,
	)
	assert.ErrorIs(t, err, ErrMemberDepartmentInactive)

	invalidMembers := []struct {
		principal guid.ID
		name      string
		email     string
		status    MemberStatus
	}{
		{0, "User", "", MemberActive},
		{principalID, "", "", MemberActive},
		{principalID, "User", "bad", MemberActive},
		{principalID, "User", "", "unknown"},
	}
	for _, item := range invalidMembers {
		_, err = harness.service.PutTenantMember(
			t.Context(), tenantID, item.principal, item.name, item.email, 0, item.status,
		)
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, filter := range []MemberFilter{
		{Limit: 0}, {Limit: 201}, {Limit: 1, Offset: -1}, {Limit: 1, Status: "bad"},
	} {
		_, _, err = harness.service.ListTenantMembers(t.Context(), tenantID, filter)
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err = harness.service.GetTenantMember(t.Context(), tenantID, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.SetTenantMemberStatus(t.Context(), tenantID, principalID, "bad")
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestServiceMenuCRUDValidationAndFailClosed(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	root, err := harness.service.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, Label: "IAM", Route: "/iam", Status: MenuActive,
	})
	require.NoError(t, err)
	child, err := harness.service.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, ParentID: root.ID, Label: "Users", Route: "/iam/users",
		PermissionCode: "user_view", Status: MenuActive,
	})
	require.NoError(t, err)
	menus, err := harness.service.ListMenus(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Len(t, menus, 2)
	stored, err := harness.service.GetMenu(t.Context(), tenantID, child.ID)
	require.NoError(t, err)
	stored.Label = "People"
	updated, err := harness.service.UpdateMenu(t.Context(), stored)
	require.NoError(t, err)
	assert.Equal(t, "People", updated.Label)
	_, err = harness.service.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, Label: "Duplicate", Route: "/iam/users", Status: MenuActive,
	})
	assert.ErrorIs(t, err, ErrMenuConflict)

	invalidMenus := []Menu{
		{TenantID: tenantID, Label: "", Status: MenuActive},
		{TenantID: tenantID, Label: "Bad", Route: "relative", Status: MenuActive},
		{TenantID: tenantID, Label: "Bad", Route: "/bad//route", Status: MenuActive},
		{TenantID: tenantID, Label: "Bad", PermissionCode: "missing", Status: MenuActive},
		{TenantID: tenantID, Label: "Bad", ParentID: 999, Status: MenuActive},
		{TenantID: tenantID, Label: "Bad", Status: "unknown"},
	}
	for _, menu := range invalidMenus {
		_, err = harness.service.CreateMenu(t.Context(), menu)
		assert.Error(t, err)
	}

	require.NoError(t, harness.service.DeleteMenu(t.Context(), tenantID, child.ID))
	require.NoError(t, harness.service.DeleteMenu(t.Context(), tenantID, root.ID))
	_, err = harness.service.GetMenu(t.Context(), tenantID, root.ID)
	assert.ErrorIs(t, err, ErrMenuNotFound)

	_, err = harness.repository.CreateMenu(t.Context(), Menu{
		TenantID: tenantID, Label: "Corrupt", Route: "/corrupt",
		PermissionCode: "unknown_persisted_permission", Status: MenuActive,
	})
	require.NoError(t, err)
	_, err = harness.service.EffectiveMenus(t.Context(), tenantID, principalID)
	assert.ErrorContains(t, err, "unknown persisted menu permission")
}

func TestServiceEntityCRUDValidationAndBindings(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Entity role", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "entity_view")
	require.NoError(t, err)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: " Unit ", Metadata: map[string]any{"region": "west"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Unit", entity.Name)
	entities, err := harness.service.ListEntities(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, entities, 1)
	stored, err := harness.service.GetEntity(t.Context(), tenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "west", stored.Metadata["region"])

	name, entityType, status := "Updated", "department", EntityDisabled
	metadata := map[string]any{"active": false}
	updated, err := harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{
		Name: &name, Type: &entityType, Status: &status, Metadata: &metadata,
	})
	require.NoError(t, err)
	assert.Equal(t, name, updated.Name)
	assert.Equal(t, entityType, updated.Type)
	assert.Equal(t, status, updated.Status)

	status = EntityActive
	updated, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{Status: &status})
	require.NoError(t, err)
	_, changed, err := harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, entity.ID, role.ID, principalID, BindingAllow, time.Now().Add(time.Hour),
	)
	require.NoError(t, err)
	assert.True(t, changed)
	bindings, err := harness.service.ListEntityRoleBindings(t.Context(), tenantID, entity.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.False(t, bindings[0].ExpiresAt.IsZero())
	assert.ErrorIs(t, harness.service.DeleteEntity(t.Context(), tenantID, entity.ID), ErrEntityHasBindings)
	changed, err = harness.service.DeleteEntityRoleBinding(t.Context(), tenantID, entity.ID, role.ID, principalID)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.service.DeleteEntityRoleBinding(t.Context(), tenantID, entity.ID, role.ID, principalID)
	require.NoError(t, err)
	assert.False(t, changed)
	require.NoError(t, harness.service.DeleteEntity(t.Context(), tenantID, entity.ID))

	for _, invalid := range []Entity{
		{},
		{TenantID: tenantID, Type: "Bad Type", Name: "Name"},
		{TenantID: tenantID, Type: "unit", Name: ""},
		{TenantID: tenantID, Type: "unit", Name: "Name", Status: "unknown"},
	} {
		_, err = harness.service.CreateEntity(t.Context(), invalid)
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, 999, EntityPatch{})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, 999, role.ID, principalID, BindingEffect("bad"), time.Time{},
	)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestServicePlatformRestrictedPermissionsAndLifecycle(t *testing.T) {
	harness := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 21)
	harness.seedAdditionalMember(t, 1, 22, MemberActive)

	_, err := harness.service.SetPlatformAdministrator(t.Context(), 21, true, nil)
	require.NoError(t, err)
	restricted, err := harness.service.SetPlatformAdministrator(
		t.Context(), 22, false, []string{"tenant_view", "tenant_view", "tenant_create"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"tenant_create", "tenant_view"}, restricted.Permissions)
	allowed, err := harness.authorizer.CheckPlatform(t.Context(), "tenant_view", 22)
	require.NoError(t, err)
	assert.True(t, allowed)
	for _, code := range []string{"tenant_delete", "platform_administer", "store_view", "missing"} {
		allowed, err = harness.authorizer.CheckPlatform(t.Context(), code, 22)
		require.NoError(t, err)
		assert.False(t, allowed, code)
	}

	admins, err := harness.service.ListPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.Len(t, admins, 2)
	changed, err := harness.service.DeletePlatformAdministrator(t.Context(), 22)
	require.NoError(t, err)
	assert.True(t, changed)
	_, err = harness.service.DeletePlatformAdministrator(t.Context(), 22)
	assert.ErrorIs(t, err, ErrPlatformAdministratorNotFound)

	invalid := []struct {
		principal   guid.ID
		full        bool
		permissions []string
		want        error
	}{
		{0, true, nil, ErrInvalidArgument},
		{22, false, nil, ErrInvalidArgument},
		{22, true, []string{"tenant_view"}, ErrInvalidArgument},
		{22, false, []string{"store_view"}, ErrPlatformPermissionScope},
		{22, false, []string{"missing"}, ErrPermissionNotFound},
	}
	for _, item := range invalid {
		_, err = harness.service.SetPlatformAdministrator(
			t.Context(), item.principal, item.full, item.permissions,
		)
		assert.ErrorIs(t, err, item.want)
	}
	assert.Equal(t, PlatformPermissionCodes(harness.service.registry), func() []string {
		codes := make([]string, 0)
		for _, action := range harness.service.PlatformPermissionCatalog(t.Context()) {
			codes = append(codes, action.Code)
		}
		return codes
	}())
	assert.Empty(t, PlatformPermissionCodes(nil))
}

func TestServiceCatalogsAndConstructors(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantCatalog := harness.service.PermissionCatalog(t.Context())
	require.NotEmpty(t, tenantCatalog)
	for _, action := range tenantCatalog {
		assert.NotEqual(t, "platform", action.Scope)
	}
	assert.Len(t, harness.service.ScopeModel(t.Context()), 4)
	menuCatalog := harness.service.MenuCatalog(t.Context())
	require.Len(t, menuCatalog, 1)
	assert.NotEmpty(t, menuCatalog[0].Children)
	assert.NotEmpty(t, DefaultMenus())
	assert.NotNil(t, newDeclarationService(harness.service.registry))

	assert.Panics(t, func() {
		NewService(nil, harness.repository, harness.authorizer, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	})
	assert.Panics(t, func() { NewAuthorizerWithRegistry(nil, harness.service.registry) })
	assert.Panics(t, func() { NewAuthorizerWithRegistry(harness.runtime, nil) })
	assert.Equal(t, Name, (&Module{}).ModuleName())
	require.Len(t, (&Module{}).MigrationMilestones(), 1)
}

func TestServiceRoleConditionValidationAndReadModels(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Conditional", "")
	require.NoError(t, err)
	roles, err := harness.service.ListRoles(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, role.ID, roles[0].ID)

	_, _, err = harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view",
		[]byte(`{"version":1,"eq":[{"context":"client.id"},{"value":"console"}]}`),
	)
	assert.ErrorIs(t, err, ErrRolePermissionNotGranted)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "platform_administer")
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "missing")
	assert.ErrorIs(t, err, ErrPermissionNotFound)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	_, _, err = harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view", []byte(`{"version":9}`),
	)
	assert.ErrorIs(t, err, ErrInvalidRolePermissionCondition)
	grant, changed, err := harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view",
		[]byte(`{"version":1,"eq":[{"context":"client.id"},{"value":"console"}]}`),
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.NotEmpty(t, grant.Condition)
	_, changed, err = harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view", grant.Condition,
	)
	require.NoError(t, err)
	assert.False(t, changed)
	grant, changed, err = harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view", nil,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, grant.Condition)

	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, 999)
	assert.ErrorIs(t, err, ErrMemberInactive)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.ListPermissions(t.Context(), tenantID, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.ListMembers(t.Context(), tenantID, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestServiceRoleDataScopeValidationAndAtomicMemberDepartment(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	seedTenantAndPrincipal(t, harness.db, "sl_", tenantID, principalID)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?,101,NULL,'Active','active','active',0,1,?,?),
		       (?,102,NULL,'Disabled','disabled','disabled',0,1,?,?)`,
		tenantID, now, now, tenantID, now, now).Error)
	_, err := harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, "Member", "", 101, MemberActive,
	)
	require.NoError(t, err)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Scoped", "")
	require.NoError(t, err)
	defaultScope, err := harness.service.GetRoleDataScope(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, DataScopeAll, defaultScope.Scope)
	assert.NotNil(t, defaultScope.DepartmentIDs)

	for _, item := range []struct {
		scope DataScope
		ids   []guid.ID
		want  error
	}{
		{DataScopeSelectedDepartments, nil, ErrRoleScopeDepartmentNeeded},
		{DataScopeAll, []guid.ID{101}, ErrRoleScopeDepartmentsExtra},
		{DataScope("unknown"), nil, ErrInvalidRoleDataScope},
		{DataScopeSelectedDepartments, []guid.ID{0}, ErrInvalidRoleDataScope},
		{DataScopeSelectedDepartments, []guid.ID{999}, ErrRoleScopeDepartmentMissing},
		{DataScopeSelectedDepartments, []guid.ID{102}, ErrRoleScopeDepartmentInactive},
	} {
		_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantID, role.ID, item.scope, item.ids)
		assert.ErrorIs(t, err, item.want)
	}
	result, changed, err := harness.service.SetRoleDataScope(
		t.Context(), tenantID, role.ID, DataScopeSelectedDepartments, []guid.ID{101},
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []guid.ID{101}, result.DepartmentIDs)
	result, changed, err = harness.service.SetRoleDataScope(t.Context(), tenantID, role.ID, DataScopeAll, nil)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, result.DepartmentIDs)

	_, err = harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, "Should Roll Back", "", 999, MemberActive,
	)
	assert.ErrorIs(t, err, ErrMemberDepartmentMissing)
	stored, err := harness.service.GetTenantMember(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	assert.Equal(t, "Member", stored.DisplayName)
	assert.Equal(t, guid.ID(101), stored.DepartmentID)
	stored, err = harness.service.PutTenantMember(
		t.Context(), tenantID, principalID, "Member", "", 0, MemberActive,
	)
	require.NoError(t, err)
	assert.Zero(t, stored.DepartmentID)
}

func TestServiceDirectoryBindingValidationAndIdempotency(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID := guid.ID(1)
	seedTenantAndPrincipal(t, harness.db, "sl_", tenantID, 11)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Directory role", "")
	require.NoError(t, err)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (?,201,'Active','active','static','','','active',0,1,?,?),
		       (?,202,'Disabled','disabled','static','','','disabled',0,1,?,?)`,
		tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_positions
		(tenant_id,id,code,name,status,sort_order,version,created_at,updated_at)
		VALUES (?,301,'active','Active','active',0,1,?,?),
		       (?,302,'disabled','Disabled','disabled',0,1,?,?)`,
		tenantID, now, now, tenantID, now, now).Error)

	for _, item := range []struct {
		typeName DirectoryAssigneeType
		id       guid.ID
		want     error
	}{
		{"bad", 201, ErrInvalidArgument},
		{DirectoryAssigneeGroup, 0, ErrInvalidArgument},
		{DirectoryAssigneeGroup, 999, ErrDirectoryAssigneeNotFound},
		{DirectoryAssigneePosition, 999, ErrDirectoryAssigneeNotFound},
		{DirectoryAssigneeGroup, 202, ErrDirectoryAssigneeInactive},
		{DirectoryAssigneePosition, 302, ErrDirectoryAssigneeInactive},
	} {
		_, err = harness.service.AddDirectoryBinding(t.Context(), tenantID, role.ID, item.typeName, item.id)
		assert.ErrorIs(t, err, item.want)
	}
	changed, err := harness.service.AddDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneePosition, 301,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.service.AddDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneePosition, 301,
	)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = harness.service.RemoveDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneePosition, 301,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.service.RemoveDirectoryBinding(
		t.Context(), tenantID, role.ID, DirectoryAssigneePosition, 301,
	)
	require.NoError(t, err)
	assert.False(t, changed)
	_, err = harness.service.ListDirectoryBindings(t.Context(), tenantID, 999)
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestServiceListsPermanentAndTemporaryMemberRoles(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	permanent, err := harness.service.CreateRole(t.Context(), tenantID, "Permanent", "")
	require.NoError(t, err)
	temporary, err := harness.service.CreateRole(t.Context(), tenantID, "Temporary", "")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, permanent.ID, principalID)
	require.NoError(t, err)
	require.NoError(t, harness.repository.GrantTemporaryRole(
		t.Context(), tenantID, 301, temporary.ID, principalID, principalID,
		repositoryTestTime.Add(-time.Minute), repositoryTestTime.Add(time.Hour),
	))
	roles, err := harness.service.ListTenantMemberRoles(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{permanent.ID, temporary.ID}, roles)
	_, err = harness.service.ListTenantMemberRoles(t.Context(), tenantID, 999)
	assert.ErrorIs(t, err, ErrMemberNotFound)

	assert.Error(t, harness.repository.GrantTemporaryRole(
		t.Context(), 0, 301, temporary.ID, principalID, principalID,
		repositoryTestTime, repositoryTestTime.Add(time.Hour),
	))
	assert.Error(t, harness.repository.GrantTemporaryRole(
		t.Context(), tenantID, 302, temporary.ID, principalID, principalID,
		repositoryTestTime.Add(time.Hour), repositoryTestTime,
	))
	_, err = harness.repository.RevokeTemporaryRole(t.Context(), 0, 301)
	assert.Error(t, err)
	assert.ErrorIs(t, harness.repository.grantTemporaryRoleOn(
		t.Context(), nil, tenantID, 303, temporary.ID, principalID, principalID,
		repositoryTestTime, repositoryTestTime.Add(time.Hour),
	), ErrUnavailable)
	_, err = harness.repository.revokeTemporaryRoleOn(t.Context(), nil, tenantID, 301)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestServiceWriteRollsBackMutationRevisionAndAuditFailure(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	authorizer := NewAuthorizer(harness.runtime)
	want := errors.New("audit unavailable")
	service := NewService(
		authz.DefaultRegistry(), harness.repository, authorizer,
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return want },
	)
	_, err := service.CreateRole(t.Context(), 1, "Rolled back", "")
	assert.ErrorIs(t, err, want)
	roles, err := harness.repository.ListRoles(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, roles)
	revision, err := harness.repository.revisions.Current(t.Context(), harness.db, 1)
	require.NoError(t, err)
	assert.Zero(t, revision)
}

func TestServiceEntityConflictsMetadataRelationshipsAndBindingValidation(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Entity manager", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "entity_view")
	require.NoError(t, err)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: "Primary",
	})
	require.NoError(t, err)
	_, err = harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: " PRIMARY ",
	})
	assert.ErrorIs(t, err, ErrEntityConflict)
	_, err = harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: "Invalid metadata",
		Metadata: map[string]any{"channel": make(chan int)},
	})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: "Large metadata",
		Metadata: map[string]any{"value": string(make([]byte, maxEntityMetadataBytes+1))},
	})
	assert.ErrorIs(t, err, ErrInvalidArgument)

	_, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	name := "missing"
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, 999, EntityPatch{Name: &name})
	assert.ErrorIs(t, err, ErrEntityNotFound)
	missingParent := guid.ID(999)
	_, err = harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, ParentID: missingParent, Type: "unit", Name: "Orphan",
	})
	assert.ErrorIs(t, err, ErrEntityNotFound)

	for _, item := range []struct {
		entityID, roleID, principalID guid.ID
		effect                        BindingEffect
		expires                       time.Time
		want                          error
	}{
		{0, role.ID, principalID, BindingAllow, time.Time{}, ErrInvalidArgument},
		{entity.ID, 0, principalID, BindingAllow, time.Time{}, ErrInvalidArgument},
		{entity.ID, role.ID, 0, BindingAllow, time.Time{}, ErrInvalidArgument},
		{entity.ID, role.ID, principalID, "bad", time.Time{}, ErrInvalidArgument},
		{entity.ID, role.ID, principalID, BindingAllow, time.Now().Add(-time.Second), ErrInvalidArgument},
		{999, role.ID, principalID, BindingAllow, time.Time{}, ErrEntityNotFound},
		{entity.ID, 999, principalID, BindingAllow, time.Time{}, ErrRoleNotFound},
		{entity.ID, role.ID, 999, BindingAllow, time.Time{}, ErrMemberInactive},
	} {
		_, _, err = harness.service.PutEntityRoleBinding(
			t.Context(), tenantID, item.entityID, item.roleID, item.principalID, item.effect, item.expires,
		)
		assert.ErrorIs(t, err, item.want)
	}

	relationship := Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "unit", ResourceID: entity.ID,
	}
	_, _, err = harness.service.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.ErrorIs(t, harness.service.DeleteEntity(t.Context(), tenantID, entity.ID), ErrEntityHasRelationships)
	newType := "department"
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{Type: &newType})
	assert.ErrorIs(t, err, ErrEntityHasRelationships)
	_, err = harness.service.DeleteRelationship(t.Context(), relationship)
	require.NoError(t, err)
	require.NoError(t, harness.service.DeleteEntity(t.Context(), tenantID, entity.ID))
	assert.ErrorIs(t, harness.service.DeleteEntity(t.Context(), tenantID, entity.ID), ErrEntityNotFound)

	corrupt, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "unit", Name: "Corrupt",
	})
	require.NoError(t, err)
	require.NoError(t, harness.db.Table("sl_iam_entities").
		Where("tenant_id = ? AND id = ?", tenantID, corrupt.ID).
		Update("metadata", "{broken").Error)
	_, err = harness.service.GetEntity(t.Context(), tenantID, corrupt.ID)
	assert.Error(t, err)
	_, err = harness.service.ListEntities(t.Context(), tenantID)
	assert.Error(t, err)
}

func TestServicePlatformAdministratorPromotionDemotionAndRepositoryHelpers(t *testing.T) {
	harness := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 21)
	harness.seedAdditionalMember(t, 1, 22, MemberActive)
	first, err := harness.service.SetPlatformAdministrator(t.Context(), 21, true, nil)
	require.NoError(t, err)
	assert.True(t, first.FullAdministrator)
	_, err = harness.service.SetPlatformAdministrator(t.Context(), 21, false, []string{"tenant_view"})
	assert.ErrorIs(t, err, ErrLastPlatformAdministrator)
	_, err = harness.service.SetPlatformAdministrator(t.Context(), 22, true, nil)
	require.NoError(t, err)
	demoted, err := harness.service.SetPlatformAdministrator(
		t.Context(), 21, false, []string{"tenant_view", "tenant_create"},
	)
	require.NoError(t, err)
	assert.False(t, demoted.FullAdministrator)
	stored, err := harness.repository.GetPlatformAdministrator(t.Context(), 21)
	require.NoError(t, err)
	assert.Equal(t, []string{"tenant_create", "tenant_view"}, stored.Permissions)
	promoted, err := harness.service.SetPlatformAdministrator(t.Context(), 21, true, nil)
	require.NoError(t, err)
	assert.True(t, promoted.FullAdministrator)
	assert.Empty(t, promoted.Permissions)
	count, err := harness.repository.CountFullPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	changed, err := harness.repository.GrantPlatformAdministrator(t.Context(), 22)
	require.NoError(t, err)
	assert.False(t, changed)
	_, err = harness.repository.GrantPlatformAdministrator(t.Context(), 0)
	assert.Error(t, err)
	_, err = harness.repository.GetPlatformAdministrator(t.Context(), 999)
	assert.ErrorIs(t, err, ErrPlatformAdministratorNotFound)
}

func TestServiceRoleAndMenuUpdateValidationBranches(t *testing.T) {
	harness := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	role, err := harness.service.CreateRole(t.Context(), 1, "Role", "")
	require.NoError(t, err)
	empty := "  "
	_, err = harness.service.UpdateRole(t.Context(), 1, role.ID, &empty, nil)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	tooLong := string(make([]byte, 4097))
	_, err = harness.service.UpdateRole(t.Context(), 1, role.ID, nil, &tooLong)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.UpdateRole(t.Context(), 1, 0, &empty, nil)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	assert.ErrorIs(t, harness.service.DeleteRole(t.Context(), 1, 999), ErrRoleNotFound)

	menu, err := harness.service.CreateMenu(t.Context(), Menu{
		TenantID: 1, Label: "  Menu  ", Route: "  /menu  ", Icon: "  boxes  ",
		PermissionCode: "  menu_view  ", Status: MenuActive,
	})
	require.NoError(t, err)
	assert.Equal(t, "Menu", menu.Label)
	assert.Equal(t, "/menu", menu.Route)
	assert.Equal(t, "boxes", menu.Icon)
	assert.Equal(t, "menu_view", menu.PermissionCode)
	_, err = harness.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err, "normalized permission code must remain resolvable")
	menu.Label = ""
	_, err = harness.service.UpdateMenu(t.Context(), menu)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	menu.Label, menu.ParentID = "Menu", 999
	_, err = harness.service.UpdateMenu(t.Context(), menu)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.UpdateMenu(t.Context(), Menu{TenantID: 1, ID: 999, Label: "Missing", Status: MenuActive})
	assert.ErrorIs(t, err, ErrMenuNotFound)
	_, err = harness.service.GetMenu(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	assert.ErrorIs(t, harness.service.DeleteMenu(t.Context(), 1, 999), ErrMenuNotFound)
}

func TestConcurrentHierarchyUpdatesCannotCreateCycles(t *testing.T) {
	harness := newCoreServiceHarness(t)
	harness.seedMember(t, 1, 11, MemberActive)
	service := NewService(authz.DefaultRegistry(), harness.repository, harness.authorizer,
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	a, err := service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "unit", Name: "A"})
	require.NoError(t, err)
	b, err := service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "unit", Name: "B"})
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, update := range []struct{ id, parent guid.ID }{{a.ID, b.ID}, {b.ID, a.ID}} {
		go func() {
			<-start
			_, updateErr := service.UpdateEntity(
				context.Background(), 1, update.id, EntityPatch{ParentID: &update.parent},
			)
			results <- updateErr
		}()
	}
	close(start)
	errorsSeen := []error{<-results, <-results}
	assert.Equal(t, 1, countErrors(errorsSeen, nil))
	assert.Equal(t, 1, countErrors(errorsSeen, ErrEntityHierarchy))

	storedA, err := service.GetEntity(t.Context(), 1, a.ID)
	require.NoError(t, err)
	storedB, err := service.GetEntity(t.Context(), 1, b.ID)
	require.NoError(t, err)
	assert.False(t, storedA.ParentID == b.ID && storedB.ParentID == a.ID)

	menuA, err := service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "A", Status: MenuActive})
	require.NoError(t, err)
	menuB, err := service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "B", Status: MenuActive})
	require.NoError(t, err)
	start = make(chan struct{})
	results = make(chan error, 2)
	for _, menu := range []Menu{
		{TenantID: 1, ID: menuA.ID, ParentID: menuB.ID, Label: "A", Status: MenuActive},
		{TenantID: 1, ID: menuB.ID, ParentID: menuA.ID, Label: "B", Status: MenuActive},
	} {
		go func() {
			<-start
			_, updateErr := service.UpdateMenu(context.Background(), menu)
			results <- updateErr
		}()
	}
	close(start)
	errorsSeen = []error{<-results, <-results}
	assert.Equal(t, 1, countErrors(errorsSeen, nil))
	assert.Equal(t, 1, countErrors(errorsSeen, ErrInvalidArgument))
}

func TestConcurrentIdempotentPolicyPutsConverge(t *testing.T) {
	harness := newCoreServiceHarness(t)
	harness.seedMember(t, 1, 11, MemberActive)
	service := NewService(authz.DefaultRegistry(), harness.repository, harness.authorizer,
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	entity, err := service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "unit", Name: "Unit"})
	require.NoError(t, err)
	role, err := service.CreateRole(t.Context(), 1, "Role", "")
	require.NoError(t, err)

	run := func(call func() (bool, error)) []policyPutResult {
		start := make(chan struct{})
		results := make(chan policyPutResult, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		for range 2 {
			go func() {
				defer workers.Done()
				<-start
				changed, callErr := call()
				results <- policyPutResult{changed: changed, err: callErr}
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		return []policyPutResult{<-results, <-results}
	}

	bindingResults := run(func() (bool, error) {
		_, changed, callErr := service.PutEntityRoleBinding(
			context.Background(), 1, entity.ID, role.ID, 11, BindingAllow, time.Time{},
		)
		return changed, callErr
	})
	assertPutResultsConverge(t, bindingResults)

	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (1,101,NULL,'Selected','selected','active',0,1,?,?)`, now, now).Error)
	scopeResults := run(func() (bool, error) {
		_, changed, callErr := service.SetRoleDataScope(
			context.Background(), 1, role.ID, DataScopeSelectedDepartments, []guid.ID{101},
		)
		return changed, callErr
	})
	assertPutResultsConverge(t, scopeResults)
}

func assertPutResultsConverge(t *testing.T, results []policyPutResult) {
	t.Helper()
	changed := 0
	for _, result := range results {
		require.NoError(t, result.err)
		if result.changed {
			changed++
		}
	}
	assert.Equal(t, 1, changed)
}

func countErrors(values []error, target error) int {
	count := 0
	for _, value := range values {
		if (target == nil && value == nil) || (target != nil && errors.Is(value, target)) {
			count++
		}
	}
	return count
}

func TestServiceProtectsLastTenantAdministratorAcrossMutations(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Administrators", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "tenant_administer")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)

	_, err = harness.service.RevokePermission(t.Context(), tenantID, role.ID, "tenant_administer")
	assert.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = harness.service.RemoveMember(t.Context(), tenantID, role.ID, principalID)
	assert.ErrorIs(t, err, ErrLastTenantAdministrator)
	assert.ErrorIs(t, harness.service.DeleteRole(t.Context(), tenantID, role.ID), ErrLastTenantAdministrator)
	_, _, err = harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "tenant_administer",
		[]byte(`{"version":1,"eq":[{"context":"client.id"},{"value":"console"}]}`),
	)
	assert.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = harness.service.SetTenantMemberStatus(t.Context(), tenantID, principalID, MemberDisabled)
	assert.ErrorIs(t, err, ErrLastTenantAdministrator)
	stored, err := harness.service.GetTenantMember(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	assert.Equal(t, MemberActive, stored.Status)
	permissions, err := harness.service.ListPermissions(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"tenant_administer"}, permissions)
}

func TestServiceEntityHierarchyDepthLimit(t *testing.T) {
	harness := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	var parentID guid.ID
	for depth := 0; depth < maxEntityDepth; depth++ {
		entity, err := harness.service.CreateEntity(t.Context(), Entity{
			TenantID: 1, ParentID: parentID, Type: "unit", Name: fmt.Sprintf("Depth %02d", depth),
		})
		require.NoError(t, err)
		parentID = entity.ID
	}
	_, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: 1, ParentID: parentID, Type: "unit", Name: "Too deep",
	})
	assert.ErrorIs(t, err, ErrEntityHierarchy)
}
