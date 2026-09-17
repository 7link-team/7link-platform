package core

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCorePersistenceAndAuthorizationFailClosedAfterDatabaseShutdown(t *testing.T) {
	harness := newCoreServiceHarness(t)
	sqlDB, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	ctx := t.Context()
	repo := harness.repository
	tenantID, roleID, principalID, entityID := guid.ID(1), guid.ID(2), guid.ID(3), guid.ID(4)
	now := repositoryTestTime

	checks := []struct {
		name string
		call func() error
	}{
		{"create role", func() error { _, err := repo.CreateRole(ctx, tenantID, "role", ""); return err }},
		{"list roles", func() error { _, err := repo.ListRoles(ctx, tenantID); return err }},
		{"get role", func() error { _, err := repo.GetRole(ctx, tenantID, roleID); return err }},
		{"update role", func() error { _, err := repo.UpdateRole(ctx, tenantID, roleID, "role", ""); return err }},
		{"delete role", func() error { return repo.DeleteRole(ctx, tenantID, roleID) }},
		{"grant platform administrator", func() error { _, err := repo.GrantPlatformAdministrator(ctx, principalID); return err }},
		{"grant permission", func() error { _, err := repo.GrantPermission(ctx, tenantID, roleID, "role_view"); return err }},
		{"revoke permission", func() error { _, err := repo.RevokePermission(ctx, tenantID, roleID, "role_view"); return err }},
		{"list permissions", func() error { _, err := repo.ListPermissions(ctx, tenantID, roleID); return err }},
		{"list permission grants", func() error { _, err := repo.ListPermissionGrants(ctx, tenantID, roleID); return err }},
		{"set permission condition", func() error {
			_, _, err := repo.SetPermissionCondition(ctx, tenantID, roleID, "role_view", nil)
			return err
		}},
		{"add role member", func() error { _, err := repo.AddMember(ctx, tenantID, roleID, principalID); return err }},
		{"remove role member", func() error { _, err := repo.RemoveMember(ctx, tenantID, roleID, principalID); return err }},
		{"list role members", func() error { _, err := repo.ListMembers(ctx, tenantID, roleID); return err }},
		{"put member", func() error {
			_, err := repo.PutMember(ctx, TenantMember{TenantID: tenantID, PrincipalID: principalID, DisplayName: "member", Status: MemberActive})
			return err
		}},
		{"get member", func() error { _, err := repo.GetMember(ctx, tenantID, principalID); return err }},
		{"list member page", func() error { _, _, err := repo.ListMembersPage(ctx, tenantID, MemberFilter{Limit: 1}); return err }},
		{"set member status", func() error { _, err := repo.SetMemberStatus(ctx, tenantID, principalID, MemberDisabled); return err }},
		{"check member", func() error { _, err := repo.IsMemberActive(ctx, tenantID, principalID); return err }},
		{"list member roles", func() error { _, err := repo.ListMemberRoleIDs(ctx, tenantID, principalID); return err }},
		{"create menu", func() error {
			_, err := repo.CreateMenu(ctx, Menu{TenantID: tenantID, Label: "menu", Status: MenuActive})
			return err
		}},
		{"get menu", func() error { _, err := repo.GetMenu(ctx, tenantID, roleID); return err }},
		{"list menus", func() error { _, err := repo.ListMenus(ctx, tenantID, false); return err }},
		{"update menu", func() error {
			_, err := repo.UpdateMenu(ctx, Menu{TenantID: tenantID, ID: roleID, Label: "menu", Status: MenuActive})
			return err
		}},
		{"delete menu", func() error { return repo.DeleteMenu(ctx, tenantID, roleID) }},
		{"get data scope", func() error { _, err := repo.GetRoleDataScope(ctx, tenantID, roleID); return err }},
		{"replace data scope", func() error { return repo.replaceRoleDataScope(ctx, tenantID, roleID, DataScopeAll, nil) }},
		{"set member department", func() error { return repo.setMemberDepartment(ctx, tenantID, principalID, 5) }},
		{"get department status", func() error { _, err := repo.departmentStatus(ctx, tenantID, 5); return err }},
		{"list directory bindings", func() error { _, err := repo.ListDirectoryBindings(ctx, tenantID, roleID); return err }},
		{"list directory binding page", func() error {
			_, _, err := repo.ListDirectoryBindingsPage(ctx, tenantID, roleID, 0, 1)
			return err
		}},
		{"add directory binding", func() error {
			_, err := repo.ChangeDirectoryBinding(ctx, tenantID, roleID, DirectoryAssigneeGroup, 5, true)
			return err
		}},
		{"remove directory binding", func() error {
			_, err := repo.ChangeDirectoryBinding(ctx, tenantID, roleID, DirectoryAssigneePosition, 5, false)
			return err
		}},
		{"get directory status", func() error {
			_, err := repo.directoryAssigneeStatus(ctx, tenantID, DirectoryAssigneeGroup, 5)
			return err
		}},
		{"create entity", func() error {
			_, err := repo.CreateEntity(ctx, Entity{TenantID: tenantID, Type: "unit", Name: "unit"})
			return err
		}},
		{"list entities", func() error { _, err := repo.ListEntities(ctx, tenantID); return err }},
		{"get entity", func() error { _, err := repo.GetEntity(ctx, tenantID, entityID); return err }},
		{"update entity", func() error {
			_, err := repo.UpdateEntity(ctx, Entity{TenantID: tenantID, ID: entityID, Type: "unit", Name: "unit", Status: EntityActive})
			return err
		}},
		{"delete entity", func() error { return repo.DeleteEntity(ctx, tenantID, entityID) }},
		{"list entity bindings", func() error { _, err := repo.ListEntityRoleBindings(ctx, tenantID, entityID); return err }},
		{"list entity binding page", func() error {
			_, _, err := repo.ListEntityRoleBindingsPage(ctx, tenantID, entityID, 0, 1)
			return err
		}},
		{"put entity binding", func() error {
			_, _, err := repo.PutEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID, BindingAllow, time.Time{})
			return err
		}},
		{"delete entity binding", func() error {
			_, err := repo.DeleteEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID)
			return err
		}},
		{"count entity relationships", func() error { _, err := repo.entityRelationshipCount(ctx, tenantID, entityID); return err }},
		{"list relationships", func() error { _, err := repo.ListRelationships(ctx, tenantID, RelationshipFilter{}); return err }},
		{"list relationship page", func() error {
			_, _, err := repo.ListRelationshipsPage(ctx, tenantID, RelationshipFilter{}, 0, 1)
			return err
		}},
		{"list resource relationship page", func() error {
			_, _, err := repo.ListRelationshipsPage(ctx, tenantID, RelationshipFilter{EntityID: entityID}, 0, 1)
			return err
		}},
		{"put relationship", func() error {
			_, _, err := repo.PutRelationship(ctx, Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "unit", ResourceID: entityID})
			return err
		}},
		{"delete relationship", func() error {
			_, err := repo.DeleteRelationship(ctx, Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "unit", ResourceID: entityID})
			return err
		}},
		{"list platform administrators", func() error { _, err := repo.ListPlatformAdministrators(ctx); return err }},
		{"get platform administrator", func() error { _, err := repo.GetPlatformAdministrator(ctx, principalID); return err }},
		{"set platform administrator", func() error { _, err := repo.SetPlatformAdministrator(ctx, principalID, true, nil); return err }},
		{"delete platform administrator", func() error { _, err := repo.DeletePlatformAdministrator(ctx, principalID); return err }},
		{"count platform administrators", func() error { _, err := repo.CountFullPlatformAdministrators(ctx); return err }},
		{"temporary role", func() error {
			return repo.GrantTemporaryRole(ctx, tenantID, 6, roleID, principalID, principalID, now, now.Add(time.Hour))
		}},
		{"revoke temporary role", func() error { _, err := repo.RevokeTemporaryRole(ctx, tenantID, 6); return err }},
		{"authorization check", func() error { _, err := harness.authorizer.Check(ctx, tenantID, "role_view", principalID); return err }},
		{"platform authorization", func() error { _, err := harness.authorizer.CheckPlatform(ctx, "tenant_view", principalID); return err }},
		{"authorization constraint", func() error {
			_, err := harness.authorizer.Constraint(ctx, tenantID, "store_view", principalID)
			return err
		}},
		{"entity explanation", func() error {
			_, err := harness.authorizer.ExplainEntity(ctx, tenantID, entityID, "store_view", principalID)
			return err
		}},
		{"resource explanation", func() error {
			_, err := harness.authorizer.ExplainResource(ctx, tenantID, entityID, "store", 7, "store_view", principalID)
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			assert.Error(t, check.call())
		})
	}

	checker := NewMembershipChecker(harness.runtime)
	_, err = checker.IsMemberActive(ctx, tenantID, principalID)
	assert.Error(t, err)
	_, err = checker.IsMemberActiveOn(ctx, harness.db, tenantID, principalID)
	assert.Error(t, err)
	_, _, err = tenantMembershipState(ctx, nil, testAuthorizationTables(t, "sl_"), tenantID, principalID)
	assert.ErrorIs(t, err, ErrUnavailable)

	store := NewRoleGrantStore(repo, NewAdministratorGuard(harness.runtime))
	_, err = store.RemoveGroupMembership(ctx, harness.db, tenantID, 5, principalID)
	assert.Error(t, err)
	_, err = store.RemovePositionMembership(ctx, harness.db, tenantID, 5, principalID)
	assert.Error(t, err)
	_, err = store.RemoveEntityRoleBinding(ctx, harness.db, tenantID, entityID, roleID, principalID)
	assert.Error(t, err)
}

func TestCoreUnavailableAndInvalidDependencyBranches(t *testing.T) {
	ctx := context.Background()
	var repo *Repository
	assert.Nil(t, repo.writer())
	assert.Zero(t, new(Repository).names)
	_, _, err := repo.tables()
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = repo.pinWriter()
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Error(t, repo.runInTx(ctx, nil))
	_, err = repo.nextID()
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Panics(t, func() { NewRepository(nil, newRepositoryTestIDs(0)) })
	assert.Panics(t, func() { NewRoleGrantStore(nil, nil) })

	var authorizer *Authorizer
	_, err = authorizer.Check(ctx, 1, "role_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.CheckBulk(ctx, 1, []string{"role_view"}, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.CheckPlatform(ctx, "platform_administer", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.Constraint(ctx, 1, "store_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.ExplainEntity(ctx, 1, 3, "store_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.ExplainResource(ctx, 1, 3, "store", 4, "store_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.CheckEntity(ctx, 1, 3, "store_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = authorizer.CheckResource(ctx, 1, 3, "store", 4, "store_view", 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Panics(t, func() { NewAuthorizer(nil) })
	harness := newRepositoryHarness(t, "sl_")
	assert.Panics(t, func() { NewAuthorizerWithRegistry(harness.runtime, nil) })
	var checker *MembershipChecker
	_, err = checker.IsMemberActive(ctx, 1, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Zero(t, new(MembershipChecker).names)
}

func TestRepositoryPropagatesPolicyTableFailures(t *testing.T) {
	t.Run("role permissions", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_role_permissions").Error)
		_, err = harness.repository.GrantPermission(t.Context(), 1, role.ID, "role_view")
		assert.Error(t, err)
		_, err = harness.repository.RevokePermission(t.Context(), 1, role.ID, "role_view")
		assert.Error(t, err)
		_, err = harness.repository.ListPermissionGrants(t.Context(), 1, role.ID)
		assert.Error(t, err)
		_, _, err = harness.repository.SetPermissionCondition(t.Context(), 1, role.ID, "role_view", nil)
		assert.Error(t, err)
	})

	t.Run("role members", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_role_members").Error)
		_, err = harness.repository.AddMember(t.Context(), 1, role.ID, 2)
		assert.Error(t, err)
		_, err = harness.repository.RemoveMember(t.Context(), 1, role.ID, 2)
		assert.Error(t, err)
		_, err = harness.repository.ListMembers(t.Context(), 1, role.ID)
		assert.Error(t, err)
	})

	t.Run("platform grants", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
		_, err := harness.repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_platform_grants").Error)
		_, err = harness.repository.ListPlatformAdministrators(t.Context())
		assert.Error(t, err)
		_, err = harness.repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
		assert.Error(t, err)
		_, err = harness.repository.DeletePlatformAdministrator(t.Context(), 11)
		assert.Error(t, err)
	})

	t.Run("role data scope", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
		now := repositoryTestTime
		require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
			(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
			VALUES (1,10,NULL,'Department','department','active',0,1,?,?)`, now, now).Error)
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.repository.replaceRoleDataScope(
			t.Context(), 1, role.ID, DataScopeSelectedDepartments, []guid.ID{10},
		))
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_role_scope_departments").Error)
		_, err = harness.repository.GetRoleDataScope(t.Context(), 1, role.ID)
		assert.Error(t, err)
		assert.Error(t, harness.repository.replaceRoleDataScope(
			t.Context(), 1, role.ID, DataScopeSelectedDepartments, []guid.ID{10},
		))
	})

	t.Run("role data scope root", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_role_data_scopes").Error)
		_, err = harness.repository.GetRoleDataScope(t.Context(), 1, role.ID)
		assert.Error(t, err)
		assert.Error(t, harness.repository.replaceRoleDataScope(
			t.Context(), 1, role.ID, DataScopeAll, nil,
		))
	})

	t.Run("member department", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_member_departments").Error)
		assert.Error(t, harness.repository.setMemberDepartment(t.Context(), 1, 11, 10))
	})

	t.Run("department status", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_departments").Error)
		_, err := harness.repository.departmentStatus(t.Context(), 1, 10)
		assert.Error(t, err)
	})

	t.Run("directory bindings", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_group_role_bindings").Error)
		_, err = harness.repository.ListDirectoryBindings(t.Context(), 1, role.ID)
		assert.Error(t, err)
	})

	t.Run("position bindings", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		role, err := harness.repository.CreateRole(t.Context(), 1, "Role", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_position_role_bindings").Error)
		_, err = harness.repository.ListDirectoryBindings(t.Context(), 1, role.ID)
		assert.Error(t, err)
	})

	t.Run("directory assignee status", func(t *testing.T) {
		harness := newRepositoryHarness(t, "sl_")
		require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_groups").Error)
		_, err := harness.repository.directoryAssigneeStatus(
			t.Context(), 1, DirectoryAssigneeGroup, 10,
		)
		assert.Error(t, err)
	})
}
