package organization

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryAuthorizationSourceStoreReadsGeneratedModels(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(map[string]any{
		"tenant_id": 1, "principal_id": 2, "display_name": "Member", "email": "member@example.test",
		"status": StatusActive, "created_at": organizationTestNow, "updated_at": organizationTestNow,
	}).Error)

	insertGroup := func(id guid.ID, name, groupType string) {
		require.NoError(t, h.db.Table(h.table("iam_groups")).Create(map[string]any{
			"tenant_id": 1, "id": id, "name": name, "name_key": nameKey(name), "group_type": groupType,
			"rule_json": "{}", "description": "", "status": StatusActive, "sort_order": 0, "version": 1,
			"created_at": organizationTestNow, "updated_at": organizationTestNow,
		}).Error)
	}
	insertGroup(10, "Operators", GroupTypeStatic)
	insertGroup(11, "Future", GroupTypeStatic)
	insertGroup(12, "Expired", GroupTypeStatic)
	insertGroup(13, "Permanent", GroupTypeStatic)
	require.NoError(t, h.db.Table(h.table("iam_positions")).Create(map[string]any{
		"tenant_id": 1, "id": 20, "code": "manager", "name": "Manager", "status": StatusActive,
		"sort_order": 0, "version": 1, "created_at": organizationTestNow, "updated_at": organizationTestNow,
	}).Error)

	evaluatedAt := organizationTestNow.Truncate(time.Millisecond)
	past, future, later := evaluatedAt.Add(-time.Hour), evaluatedAt.Add(time.Hour), evaluatedAt.Add(2*time.Hour)
	for _, membership := range []map[string]any{
		{"tenant_id": 1, "group_id": 10, "principal_id": 2, "starts_at": past, "ends_at": future},
		{"tenant_id": 1, "group_id": 11, "principal_id": 2, "starts_at": future, "ends_at": later},
		{"tenant_id": 1, "group_id": 12, "principal_id": 2, "starts_at": past.Add(-time.Hour), "ends_at": past},
		{"tenant_id": 1, "group_id": 13, "principal_id": 2, "starts_at": nil, "ends_at": nil},
	} {
		membership["created_at"], membership["updated_at"] = organizationTestNow, organizationTestNow
		require.NoError(t, h.db.Table(h.table("iam_group_members")).Create(membership).Error)
	}
	require.NoError(t, h.db.Table(h.table("iam_position_members")).Create(map[string]any{
		"tenant_id": 1, "position_id": 20, "principal_id": 2, "starts_at": nil, "ends_at": later,
		"created_at": organizationTestNow, "updated_at": organizationTestNow,
	}).Error)

	department, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	h.seedRole(t, 1, 30)
	require.NoError(t, h.db.Table(h.table("iam_role_data_scopes")).Create(map[string]any{
		"tenant_id": 1, "role_id": 30, "scope_type": "selected_departments", "updated_at": organizationTestNow,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_role_scope_departments")).Create(map[string]any{
		"tenant_id": 1, "role_id": 30, "department_id": department.ID, "created_at": organizationTestNow,
	}).Error)

	store := NewDirectoryAuthorizationSourceStore(h.runtime)
	expiries, err := store.ListMembershipExpiries(t.Context(), 1, 2, evaluatedAt)
	require.NoError(t, err)
	assert.Equal(t, map[guid.ID]time.Time{10: future}, expiries.Groups)
	assert.Equal(t, map[guid.ID]time.Time{20: later}, expiries.Positions)

	labels, err := store.ListLabels(t.Context(), 1, []guid.ID{30}, []guid.ID{10}, []guid.ID{20})
	require.NoError(t, err)
	assert.Equal(t, "Operators", labels.Groups[10])
	assert.Equal(t, GroupTypeStatic, labels.GroupTypes[10])
	assert.Equal(t, "Manager", labels.Positions[20])
	assert.Equal(t, []guid.ID{department.ID}, labels.RoleDepartments[30])

	empty, err := store.ListLabels(t.Context(), 1, nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty.Groups)
}

func TestDirectoryAuthorizationSourceStorePropagatesGeneratedQueryFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		table string
		call  func(*testing.T, *organizationHarness) error
	}{
		{name: "group memberships", table: "iam_group_members", call: func(t *testing.T, h *organizationHarness) error {
			_, err := NewDirectoryAuthorizationSourceStore(h.runtime).ListMembershipExpiries(t.Context(), 1, 2, organizationTestNow)
			return err
		}},
		{name: "position memberships", table: "iam_position_members", call: func(t *testing.T, h *organizationHarness) error {
			_, err := NewDirectoryAuthorizationSourceStore(h.runtime).ListMembershipExpiries(t.Context(), 1, 2, organizationTestNow)
			return err
		}},
		{name: "groups", table: "iam_groups", call: func(t *testing.T, h *organizationHarness) error {
			_, err := NewDirectoryAuthorizationSourceStore(h.runtime).ListLabels(t.Context(), 1, nil, []guid.ID{2}, nil)
			return err
		}},
		{name: "positions", table: "iam_positions", call: func(t *testing.T, h *organizationHarness) error {
			_, err := NewDirectoryAuthorizationSourceStore(h.runtime).ListLabels(t.Context(), 1, nil, nil, []guid.ID{2})
			return err
		}},
		{name: "role departments", table: "iam_role_scope_departments", call: func(t *testing.T, h *organizationHarness) error {
			_, err := NewDirectoryAuthorizationSourceStore(h.runtime).ListLabels(t.Context(), 1, []guid.ID{2}, nil, nil)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newOrganizationHarness(t, "sl_")
			require.NoError(t, h.db.Exec("DROP TABLE sl_"+test.table).Error)
			assert.Error(t, test.call(t, h))
		})
	}

	store := NewDirectoryAuthorizationSourceStore(nil)
	_, err := store.ListMembershipExpiries(t.Context(), 1, 2, organizationTestNow)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = store.ListLabels(t.Context(), 1, nil, nil, nil)
	assert.ErrorIs(t, err, ErrUnavailable)

	store = NewDirectoryAuthorizationSourceStore(appenv.NewRuntime(appenv.NewIdentity("unavailable", "1")))
	_, err = store.ListMembershipExpiries(t.Context(), 1, 2, organizationTestNow)
	assert.ErrorIs(t, err, ErrUnavailable)
}
