package core

import (
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelationshipRepositoryLifecycleIsolationAndNullableWindow(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	targetA, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "A", Status: EntityActive,
	})
	require.NoError(t, err)
	targetB, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 2, Type: "store", Name: "B", Status: EntityActive,
	})
	require.NoError(t, err)

	relationship := Relationship{
		TenantID: 2, SubjectType: "principal", SubjectID: 22,
		Relation: "viewer", ResourceType: "store", ResourceID: targetB.ID,
		CreatedAt: repositoryTestTime,
	}
	stored, changed, err := repository.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Nil(t, stored.StartsAt)
	assert.Nil(t, stored.EndsAt)

	storedAgain, changed, err := repository.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, stored.CreatedAt, storedAgain.CreatedAt)

	crossTenant := relationship
	crossTenant.TenantID = 1
	deleted, err := repository.DeleteRelationship(t.Context(), crossTenant)
	require.NoError(t, err)
	assert.False(t, deleted)
	listA, err := repository.ListRelationships(t.Context(), 1, RelationshipFilter{})
	require.NoError(t, err)
	assert.Empty(t, listA)
	listB, err := repository.ListRelationships(t.Context(), 2, RelationshipFilter{})
	require.NoError(t, err)
	require.Len(t, listB, 1)

	startsAt := repositoryTestTime.Add(time.Hour)
	endsAt := startsAt.Add(time.Hour)
	relationship.StartsAt = &startsAt
	relationship.EndsAt = &endsAt
	relationship.Condition = json.RawMessage(`{"version":1}`)
	updated, changed, err := repository.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, stored.CreatedAt, updated.CreatedAt, "upsert must preserve created_at")
	assert.Equal(t, startsAt, *updated.StartsAt)
	assert.Equal(t, endsAt, *updated.EndsAt)
	assert.JSONEq(t, `{"version":1}`, string(updated.Condition))

	assert.ErrorIs(t, repository.DeleteEntity(t.Context(), 2, targetB.ID), ErrEntityHasRelationships)
	require.NoError(t, repository.DeleteEntity(t.Context(), 1, targetA.ID))
}

func TestResourceRelationshipUsesOwningEntityAndExactTenantKey(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	owner, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "Owner", Status: EntityActive,
	})
	require.NoError(t, err)
	relationship := Relationship{
		TenantID: 1, EntityID: owner.ID, SubjectType: "principal", SubjectID: 11,
		Relation: "editor", ResourceType: "document", ResourceID: 700,
		CreatedAt: repositoryTestTime,
	}
	_, changed, err := repository.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)

	rows, err := repository.ListRelationships(t.Context(), 1, RelationshipFilter{
		EntityID: owner.ID, ResourceType: "document", ResourceID: 700,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, owner.ID, rows[0].EntityID)

	wrongTenant := relationship
	wrongTenant.TenantID = 2
	deleted, err := repository.DeleteRelationship(t.Context(), wrongTenant)
	require.NoError(t, err)
	assert.False(t, deleted)
	rows, err = repository.ListRelationships(t.Context(), 1, RelationshipFilter{EntityID: owner.ID})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.ErrorIs(t, repository.DeleteEntity(t.Context(), 1, owner.ID), ErrEntityHasRelationships)
}

// A relationship confers every action whose AllowedRelations name it, so writing
// one is a grant: the actor must already hold each of those actions on the target.
func TestPutRelationshipRequiresTheActionsTheRelationConfers(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, actorID := guid.ID(1), guid.ID(10)
	harness.seedMember(t, tenantID, actorID, MemberActive)
	store, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Store"})
	require.NoError(t, err)
	grantActor := func(name string, codes ...string) {
		role, err := harness.repository.CreateRole(t.Context(), tenantID, name, "")
		require.NoError(t, err)
		for _, code := range codes {
			_, err = harness.repository.GrantPermission(t.Context(), tenantID, role.ID, code)
			require.NoError(t, err)
		}
		_, err = harness.repository.AddMember(t.Context(), tenantID, role.ID, actorID)
		require.NoError(t, err)
	}
	grantActor("Binding managers", "entity_manage_binding")
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID})
	canDelete := func() bool {
		allowed, err := harness.authorizer.CheckEntity(t.Context(), tenantID, store.ID, "entity_delete", actorID)
		require.NoError(t, err)
		return allowed
	}

	// Control: the direct grant of the conferred action is refused.
	direct, err := harness.repository.CreateRole(t.Context(), tenantID, "Deleters", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(actor, tenantID, direct.ID, "entity_delete")
	require.ErrorIs(t, err, ErrPrivilegeEscalation)

	owner := Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: actorID, Relation: "owner", ResourceType: "store", ResourceID: store.ID}
	_, _, err = harness.service.PutRelationship(actor, owner)
	require.ErrorIs(t, err, ErrPrivilegeEscalation, "owning the entity would confer entity_delete")
	resourceOwner := owner
	resourceOwner.EntityID, resourceOwner.ResourceID = store.ID, 501
	_, _, err = harness.service.PutRelationship(actor, resourceOwner)
	require.ErrorIs(t, err, ErrPrivilegeEscalation, "a business-resource relationship confers the same actions on that resource")
	viewer := owner
	viewer.Relation = "viewer"
	_, _, err = harness.service.PutRelationship(actor, viewer)
	require.ErrorIs(t, err, ErrPrivilegeEscalation, "even viewer confers entity_view, which the actor lacks")
	items, err := harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{})
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.False(t, canDelete(), "authority is unchanged after the refused writes")

	// Holding every action the relation confers makes it grantable.
	grantActor("Viewers", "entity_view", "store_view")
	_, changed, err := harness.service.PutRelationship(actor, viewer)
	require.NoError(t, err)
	assert.True(t, changed)
	_, _, err = harness.service.PutRelationship(actor, owner)
	require.ErrorIs(t, err, ErrPrivilegeEscalation)
	assert.False(t, canDelete())

	grantActor("Administrators", "tenant_administer")
	_, changed, err = harness.service.PutRelationship(actor, owner)
	require.NoError(t, err)
	assert.True(t, changed, "a full administrator is unaffected")
}
