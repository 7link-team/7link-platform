package core

import (
	"encoding/json"
	"testing"
	"time"

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
