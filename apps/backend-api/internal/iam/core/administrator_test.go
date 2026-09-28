package core

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdministratorGuardRollsBackLastDurableAdministratorRemoval(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	seedTenantAndPrincipal(t, harness.db, "sl_", 2, 22)
	_, err := repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "First", Email: "first@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	_, err = repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 22, DisplayName: "Second", Email: "second@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	role, err := repository.CreateRole(t.Context(), 1, "Administrators", "")
	require.NoError(t, err)
	_, err = repository.GrantPermission(t.Context(), 1, role.ID, "tenant_administer")
	require.NoError(t, err)
	_, err = repository.AddMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)
	_, err = repository.AddMember(t.Context(), 1, role.ID, 22)
	require.NoError(t, err)

	store := NewRoleGrantStore(repository, boundGuard(t, harness.runtime, "sl_"))
	var removed bool
	require.NoError(t, repository.runInTx(t.Context(), func(repo *Repository) error {
		var err error
		removed, err = store.RemovePermanent(t.Context(), repo.writer(), 1, role.ID, 11, repositoryTestTime)
		return err
	}))
	assert.True(t, removed)

	err = repository.runInTx(t.Context(), func(repo *Repository) error {
		_, err := store.RemovePermanent(t.Context(), repo.writer(), 1, role.ID, 22, repositoryTestTime)
		return err
	})
	assert.ErrorIs(t, err, ErrLastTenantAdministrator)
	members, err := repository.ListMembers(t.Context(), 1, role.ID)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{22}, members, "the last administrator delete must roll back")
}

func TestRoleGrantStoreRollsBackLastDirectoryAdministratorRemoval(t *testing.T) {
	tests := []struct {
		name         string
		assigneeType DirectoryAssigneeType
		directoryID  guid.ID
		memberTable  string
		idColumn     string
	}{
		{name: "group", assigneeType: DirectoryAssigneeGroup, directoryID: 200, memberTable: "iam_group_members", idColumn: "group_id"},
		{name: "group", assigneeType: DirectoryAssigneeGroup, directoryID: 300, memberTable: "iam_group_members", idColumn: "group_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newRepositoryHarness(t, "sl_")
			repository := harness.repository
			seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
			seedDirectoryFixtures(t, harness, 1)
			_, err := repository.PutMember(t.Context(), TenantMember{
				TenantID: 1, PrincipalID: 11, DisplayName: "Administrator", Email: "admin@example.test", Status: MemberActive,
			})
			require.NoError(t, err)
			role, err := repository.CreateRole(t.Context(), 1, "Administrators", "")
			require.NoError(t, err)
			_, err = repository.GrantPermission(t.Context(), 1, role.ID, "tenant_administer")
			require.NoError(t, err)
			changed, err := repository.ChangeDirectoryBinding(t.Context(), 1, role.ID, test.assigneeType, test.directoryID, true)
			require.NoError(t, err)
			assert.True(t, changed)
			require.NoError(t, harness.db.Exec(
				"INSERT INTO sl_"+test.memberTable+" (tenant_id, "+test.idColumn+", principal_id, created_at, updated_at) VALUES (1, ?, 11, ?, ?)",
				test.directoryID, repositoryTestTime, repositoryTestTime,
			).Error)

			store := NewRoleGrantStore(repository, boundGuard(t, harness.runtime, "sl_"))
			err = repository.runInTx(t.Context(), func(repo *Repository) error {
				if test.assigneeType == DirectoryAssigneeGroup {
					_, err := store.RemoveGroupMembership(t.Context(), repo.writer(), 1, test.directoryID, 11)
					return err
				}
				_, err := store.RemoveGroupMembership(t.Context(), repo.writer(), 1, test.directoryID, 11)
				return err
			})
			assert.ErrorIs(t, err, ErrLastTenantAdministrator)
			assert.Equal(t, int64(1), testTableCount(t, harness.db, "sl_"+test.memberTable), "refused removal must roll back")
		})
	}
}

func TestPlatformAdministratorRepositoryPreservesLastFullAdministrator(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	seedSystemAccessFixture(t, harness.db, "sl_", 11, 22, 33)
	repository := harness.repository
	first, err := repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	assert.True(t, first.FullAdministrator)
	second, err := repository.SetPlatformAdministrator(t.Context(), 22, true, nil)
	require.NoError(t, err)
	assert.True(t, second.FullAdministrator)
	restricted, err := repository.SetPlatformAdministrator(t.Context(), 33, false, []string{"platform_read", "platform_write"})
	require.NoError(t, err)
	assert.False(t, restricted.FullAdministrator)
	assert.Equal(t, []string{"platform_read", "platform_write"}, restricted.Permissions)

	changed, err := repository.DeletePlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	assert.True(t, changed)
	_, err = repository.DeletePlatformAdministrator(t.Context(), 22)
	assert.ErrorIs(t, err, ErrLastPlatformAdministrator)
	count, err := repository.CountFullPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	_, err = repository.SetPlatformAdministrator(t.Context(), 22, false, []string{"platform_read"})
	assert.ErrorIs(t, err, ErrLastPlatformAdministrator)
	remaining, err := repository.GetPlatformAdministrator(t.Context(), 22)
	require.NoError(t, err)
	assert.True(t, remaining.FullAdministrator)
}

func TestConcurrentPlatformAdministratorDeletesSerializeBeforeLastAdministratorCheck(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	seedSystemAccessFixture(t, harness.db, "sl_", 11, 22)
	repository := harness.repository
	_, err := repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	_, err = repository.SetPlatformAdministrator(t.Context(), 22, true, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- repository.runInTx(ctx, func(repo *Repository) error {
			if err := repo.lockPlatformAdministratorMutations(ctx); err != nil {
				return err
			}
			close(locked)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := repo.DeletePlatformAdministrator(ctx, 11)
			return err
		})
	}()

	select {
	case <-locked:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := repository.DeletePlatformAdministrator(ctx, 22)
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		require.Failf(t, "concurrent delete did not wait for the platform lock", "error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	require.NoError(t, <-firstDone)
	assert.ErrorIs(t, <-secondDone, ErrLastPlatformAdministrator)
	count, err := repository.CountFullPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestAdministratorGuardGrantabilityFollowsTheActorsOwnAuthority(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	h.seedAdditionalMember(t, 1, 22, MemberActive)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	role := func(name string, codes ...string) guid.ID {
		created, err := h.repository.CreateRole(t.Context(), 1, name, "")
		require.NoError(t, err)
		for _, code := range codes {
			_, err = h.repository.GrantPermission(t.Context(), 1, created.ID, code)
			require.NoError(t, err)
		}
		return created.ID
	}
	held := role("Held", "menu_create")
	_, err := h.repository.AddMember(t.Context(), 1, held, 22)
	require.NoError(t, err)
	sameAuthority := role("Same authority", "menu_create")
	broader := role("Broader", "menu_create", "role_create")
	foreign, err := h.repository.CreateRole(t.Context(), 99, "System role", "")
	require.NoError(t, err)

	guard := boundGuard(t, h.runtime, "sl_")
	guard.registry = h.service.registry
	actor := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 22})
	check := func(ctx context.Context, owner guid.ID, roles ...guid.ID) error {
		return h.db.Transaction(func(tx *gorm.DB) error { return guard.RequireGrantableRoles(ctx, tx, owner, roles) })
	}

	assert.NoError(t, check(actor, 1, sameAuthority), "a role within the actor's authority is grantable")
	assert.ErrorIs(t, check(actor, 1, sameAuthority, broader), ErrPrivilegeEscalation, "one ungrantable role refuses the whole set")
	assert.ErrorIs(t, check(actor, 1, 987654321), ErrRoleNotFound)
	assert.ErrorIs(t, check(actor, 1, foreign.ID), ErrRoleNotFound, "a role of another owner is not addressable")
	assert.ErrorIs(t, check(actor, 99, sameAuthority), ErrPrivilegeEscalation, "directory grants never reach the system root")
	assert.ErrorIs(t, check(actor, 987654321), ErrInvalidArgument, "an unknown owner is refused")
	assert.NoError(t, check(t.Context(), 1, broader), "system callers without an actor are not escalation-checked")
	assert.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return guard.PrepareGrantableRoles(actor, tx, 1) }))
	assert.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return guard.PrepareGrantableRoles(actor, tx, 99) }), ErrPrivilegeEscalation)
	assert.ErrorIs(t, (*tenantAdministratorGuard)(nil).RequireGrantableRoles(actor, h.db, 1, nil), ErrUnavailable)
	assert.ErrorIs(t, guard.RequireGrantableRoles(actor, nil, 1, nil), ErrUnavailable)
	// The placement check runs on the caller's transaction through the same
	// bound guard; its semantics are TestDepartmentPlacementStaysWithinTheActorsConstraint.
	assert.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return guard.RequireGrantableDepartmentPlacement(actor, tx, 1, 0, []guid.ID{sameAuthority})
	}), "no department reaches nothing")
	assert.ErrorIs(t, (*tenantAdministratorGuard)(nil).RequireGrantableDepartmentPlacement(actor, h.db, 1, 2, nil), ErrUnavailable)
	assert.ErrorIs(t, guard.RequireGrantableDepartmentPlacement(actor, nil, 1, 2, nil), ErrUnavailable)

	withoutRegistry := boundGuard(t, h.runtime, "sl_")
	assert.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return withoutRegistry.RequireGrantableRoles(actor, tx, 1, []guid.ID{sameAuthority})
	}), ErrUnavailable, "an unset registry fails closed instead of reading a private catalog")
	members, err := h.repository.ListMembers(t.Context(), 1, broader)
	require.NoError(t, err)
	assert.Empty(t, members, "grantability checks never assign roles")
}
