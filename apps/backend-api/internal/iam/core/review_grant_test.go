package core

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedRoleGrantedPrincipal(t *testing.T, harness repositoryHarness) *Repository {
	t.Helper()
	repository := harness.repository
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	_, err := repository.PutMember(t.Context(), TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Email: "member@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	return repository
}

// TestRoleGrantedMergesPermanentAndTemporaryHolds covers every combination the
// statement answered: a hold recorded as a permanent membership, one recorded as
// a temporary grant, one recorded both ways, and the two ways a grant can be
// recorded yet not count (expired, not started). The store binding is asserted
// alongside the method so the answer governance actually receives is the one
// under test.
func TestRoleGrantedMergesPermanentAndTemporaryHolds(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := seedRoleGrantedPrincipal(t, harness)

	permanent, err := repository.CreateRole(t.Context(), 1, "Permanent", "")
	require.NoError(t, err)
	unexpired, err := repository.CreateRole(t.Context(), 1, "Unexpired", "")
	require.NoError(t, err)
	expired, err := repository.CreateRole(t.Context(), 1, "Expired", "")
	require.NoError(t, err)
	pending, err := repository.CreateRole(t.Context(), 1, "Pending", "")
	require.NoError(t, err)
	heldTwice, err := repository.CreateRole(t.Context(), 1, "HeldTwice", "")
	require.NoError(t, err)
	untouched, err := repository.CreateRole(t.Context(), 1, "Untouched", "")
	require.NoError(t, err)

	now := repositoryTestTime
	_, err = repository.AddMember(t.Context(), 1, permanent.ID, 11, nil)
	require.NoError(t, err)
	_, err = repository.AddMember(t.Context(), 1, heldTwice.ID, 11, nil)
	require.NoError(t, err)
	require.NoError(t, repository.GrantTemporaryRole(
		t.Context(), 1, 601, unexpired.ID, 11, 99, now.Add(-time.Hour), now.Add(time.Hour),
	))
	require.NoError(t, repository.GrantTemporaryRole(
		t.Context(), 1, 602, expired.ID, 11, 99, now.Add(-2*time.Hour), now.Add(-time.Hour),
	))
	require.NoError(t, repository.GrantTemporaryRole(
		t.Context(), 1, 603, pending.ID, 11, 99, now.Add(time.Hour), now.Add(2*time.Hour),
	))
	require.NoError(t, repository.GrantTemporaryRole(
		t.Context(), 1, 604, heldTwice.ID, 11, 99, now.Add(-time.Hour), now.Add(time.Hour),
	))

	store := NewRoleGrantStore(repository, boundGuard(t, harness.runtime, "sl_"))
	for _, test := range []struct {
		name   string
		roleID guid.ID
		want   bool
	}{
		{"permanent membership", permanent.ID, true},
		{"unexpired temporary grant", unexpired.ID, true},
		{"expired temporary grant", expired.ID, false},
		{"not yet started temporary grant", pending.ID, false},
		{"held both ways", heldTwice.ID, true},
		{"held by nobody", untouched.ID, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			granted, err := repository.roleGrantedOn(t.Context(), repository.writer(), 1, test.roleID, 11, now)
			require.NoError(t, err)
			assert.Equal(t, test.want, granted)

			bound, err := store.RoleGranted(t.Context(), repository.writer(), 1, test.roleID, 11, now)
			require.NoError(t, err)
			assert.Equal(t, test.want, bound, "the binding must expose the same answer")
		})
	}
}

// TestRoleGrantedReportsEitherSourceFailingEvenWhenTheOtherMatches pins what a
// short-circuit would silently lose. The statement this replaces was a UNION
// ALL, so both arms contributed rows and both arms had to be readable: a
// permanent hold did not excuse the temporary-grant table from existing, and a
// temporary hold did not excuse the membership table. Returning on the first
// match would turn an unreadable second source into a confident "already
// granted".
func TestRoleGrantedReportsEitherSourceFailingEvenWhenTheOtherMatches(t *testing.T) {
	for _, test := range []struct {
		name  string
		table string
	}{
		{"membership source unreadable", "sl_iam_role_members"},
		{"temporary grant source unreadable", "sl_iam_temporary_role_grants"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newRepositoryHarness(t, "sl_")
			repository := seedRoleGrantedPrincipal(t, harness)
			role, err := repository.CreateRole(t.Context(), 1, "Held", "")
			require.NoError(t, err)
			_, err = repository.AddMember(t.Context(), 1, role.ID, 11, nil)
			require.NoError(t, err)

			granted, err := repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, repositoryTestTime)
			require.NoError(t, err)
			require.True(t, granted)

			require.NoError(t, harness.db.Exec("DROP TABLE "+test.table).Error)
			granted, err = repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, repositoryTestTime)
			assert.False(t, granted)
			assert.ErrorContains(t, err, "check existing role grant")
		})
	}
}

func TestRoleGrantedRejectsIncompleteArguments(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	now := repositoryTestTime
	for _, test := range []struct {
		name        string
		db          *gorm.DB
		tenantID    guid.ID
		roleID      guid.ID
		principalID guid.ID
		now         time.Time
	}{
		{"no database", nil, 1, 2, 3, now},
		{"no tenant", repository.writer(), 0, 2, 3, now},
		{"no role", repository.writer(), 1, 0, 3, now},
		{"no principal", repository.writer(), 1, 2, 0, now},
		{"no clock", repository.writer(), 1, 2, 3, time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			granted, err := repository.roleGrantedOn(
				t.Context(), test.db, test.tenantID, test.roleID, test.principalID, test.now,
			)
			assert.False(t, granted)
			assert.ErrorIs(t, err, ErrInvalidArgument)
		})
	}
}

// TestRoleGrantedReadsThroughTheCallerTransaction is the reason this check is a
// store callback taking a *gorm.DB rather than an ordinary repository read: an
// approval flow decides and then writes against one snapshot. The row is written
// and observed inside a transaction that then rolls back, so the observation can
// only have come from that transaction.
func TestRoleGrantedReadsThroughTheCallerTransaction(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := seedRoleGrantedPrincipal(t, harness)
	role, err := repository.CreateRole(t.Context(), 1, "Uncommitted", "")
	require.NoError(t, err)

	rolledBack := errors.New("roll back")
	err = repository.runInTx(t.Context(), func(repo *Repository) error {
		_, err := repo.AddMember(t.Context(), 1, role.ID, 11, nil)
		require.NoError(t, err)
		granted, err := repo.roleGrantedOn(t.Context(), repo.writer(), 1, role.ID, 11, repositoryTestTime)
		require.NoError(t, err)
		assert.True(t, granted, "the check must see the write that shares its transaction")
		return rolledBack
	})
	assert.ErrorIs(t, err, rolledBack)

	granted, err := repository.roleGrantedOn(t.Context(), repository.writer(), 1, role.ID, 11, repositoryTestTime)
	require.NoError(t, err)
	assert.False(t, granted, "the rolled-back write must not have been visible outside it")
}
