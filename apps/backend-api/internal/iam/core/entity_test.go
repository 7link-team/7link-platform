package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntityRepositoryLifecycleIsolationAndNameKey(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	root, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "company", Name: "Root", Status: EntityActive,
		Metadata: map[string]any{"region": "west"},
	})
	require.NoError(t, err)
	child, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, ParentID: root.ID, Type: "store", Name: "Branch", Status: EntityActive,
		Metadata: map[string]any{"number": 7},
	})
	require.NoError(t, err)
	other, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 2, Type: "company", Name: "Root", Status: EntityActive,
	})
	require.NoError(t, err)

	_, err = repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "company", Name: "  ROOT ", Status: EntityActive,
	})
	assert.ErrorIs(t, err, ErrEntityConflict)
	var nameKey string
	require.NoError(t, harness.db.Table("sl_iam_entities").Select("name_key").
		Where("tenant_id = ? AND id = ?", root.TenantID, root.ID).Scan(&nameKey).Error)
	assert.Equal(t, "root", nameKey)

	_, err = repository.GetEntity(t.Context(), 1, other.ID)
	assert.ErrorIs(t, err, ErrEntityNotFound)
	other.TenantID, other.Name = 1, "Cross tenant"
	_, err = repository.UpdateEntity(t.Context(), other)
	assert.ErrorIs(t, err, ErrEntityNotFound)
	assert.ErrorIs(t, repository.DeleteEntity(t.Context(), 1, other.ID), ErrEntityNotFound)

	child.ParentID = 0
	child.Name = "Standalone"
	child.Metadata = map[string]any{"enabled": true}
	updated, err := repository.UpdateEntity(t.Context(), child)
	require.NoError(t, err)
	assert.Zero(t, updated.ParentID)
	assert.Equal(t, map[string]any{"enabled": true}, updated.Metadata)
	var parentNull int
	require.NoError(t, harness.db.Raw(
		"SELECT parent_id IS NULL FROM sl_iam_entities WHERE tenant_id = ? AND id = ?",
		child.TenantID, child.ID,
	).Scan(&parentNull).Error)
	assert.Equal(t, 1, parentNull)
}

func TestEntityRoleBindingsAreTenantScopedAndPreserveNullableExpiry(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	roleA, err := repository.CreateRole(t.Context(), 1, "Operator", "")
	require.NoError(t, err)
	roleB, err := repository.CreateRole(t.Context(), 2, "Operator", "")
	require.NoError(t, err)
	entityA, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "A", Status: EntityActive,
	})
	require.NoError(t, err)
	entityB, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 2, Type: "store", Name: "B", Status: EntityActive,
	})
	require.NoError(t, err)

	changed, binding, err := repository.PutEntityRoleBinding(
		t.Context(), 1, entityA.ID, roleA.ID, 101, BindingAllow, time.Time{},
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, binding.ExpiresAt.IsZero())

	changed, _, err = repository.PutEntityRoleBinding(
		t.Context(), 1, entityA.ID, roleA.ID, 101, BindingAllow, time.Time{},
	)
	require.NoError(t, err)
	assert.False(t, changed)

	expiresAt := repositoryTestTime.Add(24 * time.Hour)
	changed, binding, err = repository.PutEntityRoleBinding(
		t.Context(), 1, entityA.ID, roleA.ID, 101, BindingDeny, expiresAt,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, BindingDeny, binding.Effect)
	assert.Equal(t, expiresAt, binding.ExpiresAt)

	_, _, err = repository.PutEntityRoleBinding(
		t.Context(), 1, entityA.ID, roleB.ID, 101, BindingAllow, time.Time{},
	)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	_, _, err = repository.PutEntityRoleBinding(
		t.Context(), 1, entityB.ID, roleA.ID, 101, BindingAllow, time.Time{},
	)
	assert.ErrorIs(t, err, ErrEntityNotFound)

	deleted, err := repository.DeleteEntityRoleBinding(t.Context(), 2, entityA.ID, roleA.ID, 101)
	require.NoError(t, err)
	assert.False(t, deleted)
	bindings, err := repository.ListEntityRoleBindings(t.Context(), 1, entityA.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.Equal(t, guid.ID(101), bindings[0].PrincipalID)
	assert.ErrorIs(t, repository.DeleteEntity(t.Context(), 1, entityA.ID), ErrEntityHasBindings)
}

// The tenant-wide entity listing needs entity_view, which is the permission to
// administer every entity in the tenant. Belonging to one is a different thing,
// and somebody who belongs to one without that permission has to be able to see
// it -- otherwise the console shows them an empty chooser and goes hunting for
// their entity by provoking authorization failures until one stops.
func TestAdmittedEntitiesAnswersForTheCallerNotTheAdministrator(t *testing.T) {
	entities := []Entity{
		{ID: guid.ID(1), Name: "Finance"},
		{ID: guid.ID(2), Name: "Operations"},
		{ID: guid.ID(3), Name: "Logistics"},
	}

	t.Run("a grant on one entity admits that entity alone", func(t *testing.T) {
		admitted := admittedEntities(entities, authz.DataConstraint{
			ResourceIDs: []string{"2"},
		}, nil)
		require.Len(t, admitted, 1)
		assert.Equal(t, "Operations", admitted[0].Name)
	})

	t.Run("a tenant-wide grant admits every entity", func(t *testing.T) {
		assert.Len(t, admittedEntities(entities, authz.DataConstraint{AllowAll: true}, nil), 3)
	})

	// Breadth does not overrule an explicit denial: a tenant-wide grant with
	// one entity denied is two entities, not three.
	t.Run("an explicit denial survives a tenant-wide grant", func(t *testing.T) {
		admitted := admittedEntities(entities, authz.DataConstraint{
			AllowAll: true, DeniedIDs: []string{"1"},
		}, nil)
		require.Len(t, admitted, 2)
		assert.Equal(t, []string{"Operations", "Logistics"},
			[]string{admitted[0].Name, admitted[1].Name})
	})

	t.Run("a denial also removes an entity that was granted", func(t *testing.T) {
		assert.Empty(t, admittedEntities(entities, authz.DataConstraint{
			ResourceIDs: []string{"2"}, DeniedIDs: []string{"2"},
		}, nil))
	})

	t.Run("no grant admits nothing", func(t *testing.T) {
		assert.Empty(t, admittedEntities(entities, authz.DataConstraint{}, nil))
	})

	// A department-scoped grant names the department, never the entity behind
	// it. Without resolving that, somebody whose access runs through a
	// department was shown no entity at all while the entity-scoped endpoints
	// admitted them perfectly well.
	t.Run("an entity reached through a department is admitted", func(t *testing.T) {
		admitted := admittedEntities(entities, authz.DataConstraint{
			DepartmentIDs: []string{"77"},
		}, []guid.ID{guid.ID(3)})
		require.Len(t, admitted, 1)
		assert.Equal(t, "Logistics", admitted[0].Name)
	})

	t.Run("a denial also removes an entity reached through a department", func(t *testing.T) {
		assert.Empty(t, admittedEntities(entities, authz.DataConstraint{
			DepartmentIDs: []string{"77"}, DeniedIDs: []string{"3"},
		}, []guid.ID{guid.ID(3)}))
	})
}
