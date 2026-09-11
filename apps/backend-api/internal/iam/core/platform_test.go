package core

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListPlatformAdministratorsMergesBothArmsInStatementOrder pins the four
// things the merged statement decided and the Go merge now has to decide the
// same way: principals ascending, a principal that holds both a full row and
// explicit grants returned once with both, permission codes ascending rather
// than in the order they were inserted, and the full row's timestamp being the
// one the group keeps.
func TestListPlatformAdministratorsMergesBothArmsInStatementOrder(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository

	// Inserted highest principal first, and with its permissions in reverse
	// order, so neither the read order nor the insertion order can be mistaken
	// for the order the statement produced.
	restricted, err := repository.SetPlatformAdministrator(
		t.Context(), 33, false, []string{"platform_write", "platform_read"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"platform_read", "platform_write"}, restricted.Permissions,
		"the read orders codes even though the write took them the other way round")

	_, err = repository.SetPlatformAdministrator(t.Context(), 22, false, []string{"platform_read"})
	require.NoError(t, err)
	full, err := repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	require.True(t, full.FullAdministrator)

	// A principal can hold a full row and explicit grants at once: the statement
	// was a UNION ALL, so it emitted one row per hold and the caller grouped
	// them. The grant is deliberately older than the full row, so which
	// timestamp survives the group is observable.
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_platform_grants (principal_id, permission_code, created_at) VALUES (11, 'zz_administer', ?)",
		repositoryTestTime.Add(-30*24*time.Hour),
	).Error)

	administrators, err := repository.ListPlatformAdministrators(t.Context())
	require.NoError(t, err)
	require.Len(t, administrators, 3)
	assert.Equal(t, []guid.ID{11, 22, 33}, []guid.ID{
		administrators[0].PrincipalID,
		administrators[1].PrincipalID,
		administrators[2].PrincipalID,
	})
	assert.True(t, administrators[0].FullAdministrator)
	assert.Equal(t, []string{"zz_administer"}, administrators[0].Permissions)
	assert.Equal(t, repositoryTestTime, administrators[0].CreatedAt,
		"the full row's timestamp is the one the group keeps, not the grant's")
	assert.False(t, administrators[1].FullAdministrator)
	assert.Equal(t, []string{"platform_read"}, administrators[1].Permissions)
	assert.False(t, administrators[2].FullAdministrator)
	assert.Equal(t, []string{"platform_read", "platform_write"}, administrators[2].Permissions)
}

// TestListPlatformAdministratorsIsEmptyRatherThanAbsent keeps the read usable by
// a caller that serializes it: the merged statement returned no rows for an
// empty repository, and the Go merge has to return an empty list rather than a
// nil one, which would serialize as null.
func TestListPlatformAdministratorsIsEmptyRatherThanAbsent(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	administrators, err := harness.repository.ListPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, administrators)
	assert.Empty(t, administrators)

	administrators, err = harness.repository.ListPlatformAdministratorsOn(t.Context(), harness.db)
	require.NoError(t, err)
	assert.NotNil(t, administrators)
	assert.Empty(t, administrators)

	_, err = harness.repository.GetPlatformAdministrator(t.Context(), 11)
	assert.ErrorIs(t, err, ErrPlatformAdministratorNotFound)
	_, err = harness.repository.GetPlatformAdministrator(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

// TestListPlatformAdministratorsReadsBothArmsInTheCallersTransaction pins what
// running both arms in one transaction buys a caller that already has one: the
// merge sees the caller's own uncommitted rows from both sources, so a caller
// can decide against exactly the state it is about to write over.
func TestListPlatformAdministratorsReadsBothArmsInTheCallersTransaction(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository

	rolledBack := errors.New("roll back")
	err := repository.runInTx(t.Context(), func(repo *Repository) error {
		if _, err := repo.SetPlatformAdministrator(t.Context(), 11, true, nil); err != nil {
			return err
		}
		if _, err := repo.SetPlatformAdministrator(t.Context(), 22, false, []string{"platform_read"}); err != nil {
			return err
		}
		administrators, err := repo.ListPlatformAdministrators(t.Context())
		require.NoError(t, err)
		require.Len(t, administrators, 2, "both arms must read the caller's uncommitted rows")
		assert.Equal(t, guid.ID(11), administrators[0].PrincipalID)
		assert.True(t, administrators[0].FullAdministrator)
		assert.Equal(t, guid.ID(22), administrators[1].PrincipalID)
		assert.Equal(t, []string{"platform_read"}, administrators[1].Permissions)
		return rolledBack
	})
	assert.ErrorIs(t, err, rolledBack)

	administrators, err := repository.ListPlatformAdministrators(t.Context())
	require.NoError(t, err)
	assert.Empty(t, administrators, "reading must not have committed anything")
}

// TestPlatformRecordKindRankPutsGrantsBeforeTheFullRow pins the comparator's
// kind ordering directly, because the merged result cannot show it: the grouping
// loop only appends permission codes and overwrites the timestamp, so a
// principal holding both kinds comes back identical whichever way the two are
// ordered. Keeping the rank explicit is what makes the merge reproduce the
// statement's DESC clause instead of depending on which arm happened to be read
// first.
func TestPlatformRecordKindRankPutsGrantsBeforeTheFullRow(t *testing.T) {
	assert.Equal(t, 0, platformRecordKindRank("grant"))
	assert.Equal(t, 1, platformRecordKindRank("full"))
	assert.Less(t, platformRecordKindRank("grant"), platformRecordKindRank("full"))
}
