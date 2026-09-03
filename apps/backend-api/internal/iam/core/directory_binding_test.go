package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedDirectoryFixtures(t *testing.T, harness repositoryHarness, tenantID guid.ID) {
	t.Helper()
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_departments (tenant_id, id, name, name_key, status, sort_order, version, created_at, updated_at) VALUES (?, 100, 'Engineering', 'engineering', 'active', 0, 1, ?, ?)",
		tenantID, now, now,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_groups (tenant_id, id, name, name_key, group_type, rule_json, description, status, sort_order, version, created_at, updated_at) VALUES (?, 200, 'Operators', 'operators', 'static', '', '', 'active', 0, 1, ?, ?)",
		tenantID, now, now,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_positions (tenant_id, id, code, name, status, sort_order, version, created_at, updated_at) VALUES (?, 300, 'manager', 'Manager', 'active', 0, 1, ?, ?)",
		tenantID, now, now,
	).Error)
}

func TestDirectoryBindingsAndDataScopesUseTenantQualifiedKeys(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	seedTenantAndPrincipal(t, harness.db, "sl_", 2, 22)
	seedDirectoryFixtures(t, harness, 1)
	seedDirectoryFixtures(t, harness, 2)
	roleA, err := repository.CreateRole(t.Context(), 1, "Operator", "")
	require.NoError(t, err)
	roleB, err := repository.CreateRole(t.Context(), 2, "Operator", "")
	require.NoError(t, err)

	scope, err := repository.GetRoleDataScope(t.Context(), 1, roleA.ID)
	require.NoError(t, err)
	assert.Equal(t, DataScopeAll, scope.Scope)
	assert.Empty(t, scope.DepartmentIDs)
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		return repo.replaceRoleDataScope(t.Context(), 1, roleA.ID, DataScopeSelectedDepartments, []guid.ID{100})
	}))
	scope, err = repository.GetRoleDataScope(t.Context(), 1, roleA.ID)
	require.NoError(t, err)
	assert.Equal(t, DataScopeSelectedDepartments, scope.Scope)
	assert.Equal(t, []guid.ID{100}, scope.DepartmentIDs)
	_, err = repository.GetRoleDataScope(t.Context(), 1, roleB.ID)
	assert.ErrorIs(t, err, ErrRoleNotFound)

	_, err = repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		return repo.setMemberDepartment(t.Context(), 1, 11, 100)
	}))
	member, err := repository.GetMember(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(100), member.DepartmentID)

	changed, err := repository.ChangeDirectoryBinding(t.Context(), 1, roleA.ID, DirectoryAssigneeGroup, 200, true)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = repository.ChangeDirectoryBinding(t.Context(), 1, roleA.ID, DirectoryAssigneeGroup, 200, true)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = repository.ChangeDirectoryBinding(t.Context(), 1, roleA.ID, DirectoryAssigneePosition, 300, true)
	require.NoError(t, err)
	assert.True(t, changed)
	bindings, err := repository.ListDirectoryBindings(t.Context(), 1, roleA.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	assert.Equal(t, DirectoryAssigneeGroup, bindings[0].AssigneeType)
	assert.Equal(t, DirectoryAssigneePosition, bindings[1].AssigneeType)

	changed, err = repository.ChangeDirectoryBinding(t.Context(), 1, roleA.ID, DirectoryAssigneeGroup, 999, true)
	assert.ErrorIs(t, err, ErrDirectoryAssigneeNotFound)
	assert.False(t, changed)
	_, err = repository.ListDirectoryBindings(t.Context(), 1, roleB.ID)
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestMatchingDynamicGroupsUsesMemberFactsWithinTenant(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	seedTenantAndPrincipal(t, harness.db, "sl_", 2, 22)
	seedDirectoryFixtures(t, harness, 1)
	_, err := harness.repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	require.NoError(t, harness.repository.runInTx(t.Context(), func(repo *Repository) error {
		return repo.setMemberDepartment(t.Context(), 1, 11, 100)
	}))
	rule := `{"version":1,"match":"all","conditions":[{"field":"member.department_id","operator":"in","values":["100"]},{"field":"member.email_domain","operator":"in","values":["example.test"]}]}`
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_groups (tenant_id, id, name, name_key, group_type, rule_json, description, status, sort_order, version, created_at, updated_at) VALUES (1, 201, 'Dynamic', 'dynamic', 'dynamic', ?, '', 'active', 0, 1, ?, ?)",
		rule, repositoryTestTime, repositoryTestTime,
	).Error)

	ids, err := matchingDynamicGroupIDs(t.Context(), harness.db, "sl_", 1, 11)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{201}, ids)
	ids, err = matchingDynamicGroupIDs(t.Context(), harness.db, "sl_", 2, 11)
	require.NoError(t, err)
	assert.Empty(t, ids, "another tenant must not observe or evaluate tenant 1's dynamic group")
}
