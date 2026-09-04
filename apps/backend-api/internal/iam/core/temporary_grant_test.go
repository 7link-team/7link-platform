package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporaryRoleGrantUsesRealTimeAndTenantMembership(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	seedTenantAndPrincipal(t, harness.db, "sl_", 2, 22)
	role, err := repository.CreateRole(t.Context(), 1, "Temporary", "")
	require.NoError(t, err)
	startsAt := repositoryTestTime.Add(-time.Hour)
	endsAt := repositoryTestTime.Add(time.Hour)

	err = repository.GrantTemporaryRole(t.Context(), 1, 500, role.ID, 11, 99, startsAt, endsAt)
	assert.ErrorIs(t, err, ErrMemberInactive)
	_, err = repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	require.NoError(t, repository.GrantTemporaryRole(t.Context(), 1, 500, role.ID, 11, 99, startsAt, endsAt))

	roleIDs, err := repository.ListMemberRoleIDs(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{role.ID}, roleIDs)
	var row struct {
		StartsAt time.Time `gorm:"column:starts_at"`
		EndsAt   time.Time `gorm:"column:ends_at"`
	}
	require.NoError(t, harness.db.Table("sl_iam_temporary_role_grants").
		Where("tenant_id = 1 AND id = 500").Take(&row).Error)
	assert.Equal(t, startsAt, row.StartsAt.UTC())
	assert.Equal(t, endsAt, row.EndsAt.UTC())

	changed, err := repository.RevokeTemporaryRole(t.Context(), 2, 500)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = repository.RevokeTemporaryRole(t.Context(), 1, 500)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestRoleGrantStoreRemovesExactScopedAssignments(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	seedDirectoryFixtures(t, harness, 1)
	_, err := repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	role, err := repository.CreateRole(t.Context(), 1, "Scoped", "")
	require.NoError(t, err)
	entity, err := repository.CreateEntity(t.Context(), Entity{
		TenantID: 1, Type: "store", Name: "Store", Status: EntityActive,
	})
	require.NoError(t, err)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_group_members (tenant_id, group_id, principal_id, created_at, updated_at) VALUES (1, 200, 11, ?, ?)",
		repositoryTestTime, repositoryTestTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_position_members (tenant_id, position_id, principal_id, created_at, updated_at) VALUES (1, 300, 11, ?, ?)",
		repositoryTestTime, repositoryTestTime,
	).Error)
	_, _, err = repository.PutEntityRoleBinding(t.Context(), 1, entity.ID, role.ID, 11, BindingAllow, time.Time{})
	require.NoError(t, err)
	store := NewRoleGrantStore(repository, NewAdministratorGuard(harness.runtime))

	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		changed, err := store.RemoveGroupMembership(t.Context(), repo.writer(), 1, 200, 11)
		assert.True(t, changed)
		return err
	}))
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		changed, err := store.RemoveGroupMembership(t.Context(), repo.writer(), 1, 200, 11)
		assert.False(t, changed)
		return err
	}))
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		changed, err := store.RemovePositionMembership(t.Context(), repo.writer(), 1, 300, 11)
		assert.True(t, changed)
		return err
	}))
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		changed, err := store.RemoveEntityRoleBinding(t.Context(), repo.writer(), 1, entity.ID, role.ID, 11)
		assert.True(t, changed)
		return err
	}))
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		changed, err := store.RemoveEntityRoleBinding(t.Context(), repo.writer(), 1, entity.ID, role.ID, 11)
		assert.False(t, changed)
		return err
	}))
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_iam_group_members"))
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_iam_position_members"))
	assert.Equal(t, int64(0), testTableCount(t, harness.db, "sl_iam_role_bindings"))
}
