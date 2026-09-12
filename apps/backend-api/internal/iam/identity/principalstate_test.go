package identity

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestPrincipalStateStoreReadsTheOwnersTableUnderTheDeploymentPrefix is the
// point of the contract: a consumer resolves one capability instead of naming
// the principal table, and the prefix still applies exactly once.
func TestPrincipalStateStoreReadsTheOwnersTableUnderTheDeploymentPrefix(t *testing.T) {
	harness := newIdentityHarness(t, "principalstate_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject", Email: "subject@example.test",
	}, identityTestNow)
	require.NoError(t, err)

	store := (&Module{runtime: harness.runtime}).principalStateStore()

	status, found, err := store.Status(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "active", status)

	_, found, err = store.Status(t.Context(), harness.db, id+1)
	require.NoError(t, err, "an id nobody holds is an absence, not a failure")
	assert.False(t, found)
}

// TestPrincipalStateStoreStatusReportsAnUnreadableTableAsAFailure keeps a
// missing table from reading as a missing principal, which a caller rejects for
// an entirely different reason.
func TestPrincipalStateStoreStatusReportsAnUnreadableTableAsAFailure(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatebad_")
	store := principalStateStore(harness.prefix)

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.prefix+"iam_principals").Error)
	_, _, err := store.Status(t.Context(), harness.db, 1)
	assert.ErrorContains(t, err, "load principal status")
}

// principalStateRow is the two columns the locks must leave alone.
type principalStateRow struct {
	Status    string
	UpdatedAt string
}

// TestPrincipalStateStoreLocksWithoutChangingState covers both lock shapes. The
// lock is a write-back of the current value, so it must leave the row's status
// and timestamp exactly as they were while still taking the row.
func TestPrincipalStateStoreLocksWithoutChangingState(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatelock_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject",
	}, identityTestNow)
	require.NoError(t, err)
	store := principalStateStore(harness.prefix)

	read := func() principalStateRow {
		var row principalStateRow
		require.NoError(t, harness.db.Table(harness.prefix+"iam_principals").
			Select("status, updated_at").Where("id = ?", id).Take(&row).Error)
		return row
	}
	before := read()

	require.NoError(t, store.Lock(t.Context(), harness.db, id))
	require.NoError(t, store.LockActive(t.Context(), harness.db, id))
	assert.Equal(t, before, read(), "a lock must take the row without rewriting it")

	// A principal that is not active is left unlocked, and the read that follows
	// is what rejects it -- so neither lock reports the absence as an error.
	require.NoError(t, harness.db.Exec(
		"UPDATE "+harness.prefix+"iam_principals SET status = 'disabled' WHERE id = ?", id,
	).Error)
	require.NoError(t, store.LockActive(t.Context(), harness.db, id))
	require.NoError(t, store.Lock(t.Context(), harness.db, id+1))

	status, found, err := store.Status(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "disabled", status)
}

// TestPrincipalStateStoreRunsInTheCallersTransaction proves the lock and the
// read that follows share the snapshot the caller opened, which is what makes a
// suspension unable to land between the two.
func TestPrincipalStateStoreRunsInTheCallersTransaction(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatetx_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject",
	}, identityTestNow)
	require.NoError(t, err)
	store := principalStateStore(harness.prefix)

	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"UPDATE "+harness.prefix+"iam_principals SET status = 'disabled' WHERE id = ?", id,
		).Error; err != nil {
			return err
		}
		status, found, err := store.Status(t.Context(), tx, id)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "disabled", status)
		return nil
	}))
}

// TestPrincipalStateStoreRefusesToServeBeforeWiring covers a module that was
// never given a runtime: it must refuse rather than fall back to an unprefixed
// table that belongs to nobody.
func TestPrincipalStateStoreRefusesToServeBeforeWiring(t *testing.T) {
	harness := newIdentityHarness(t, "pstateunwired_")
	for name, unwired := range map[string]*Module{
		"nil module": nil,
		"no runtime": {},
	} {
		t.Run(name, func(t *testing.T) {
			store := unwired.principalStateStore()
			assert.ErrorIs(t, store.Lock(t.Context(), harness.db, 1), ErrUnavailable)
			assert.ErrorIs(t, store.LockActive(t.Context(), harness.db, 1), ErrUnavailable)
			status, found, err := store.Status(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, ErrUnavailable)
			assert.False(t, found)
			assert.Empty(t, status)
			for reader, read := range map[string]func() (Principal, bool, error){
				"active row":      func() (Principal, bool, error) { return store.ActiveRow(t.Context(), harness.db, 1) },
				"lock active row": func() (Principal, bool, error) { return store.LockActiveRow(t.Context(), harness.db, 1) },
				"lock by login":   func() (Principal, bool, error) { return store.LockByLogin(t.Context(), harness.db, "x") },
				"human by login":  func() (Principal, bool, error) { return store.HumanByLogin(t.Context(), harness.db, "x") },
			} {
				principal, found, err := read()
				assert.ErrorIs(t, err, ErrUnavailable, reader)
				assert.False(t, found, reader)
				assert.Zero(t, principal, reader)
			}
			people, err := store.ActiveHumanByIdentifier(t.Context(), harness.db, "x")
			assert.ErrorIs(t, err, ErrUnavailable)
			assert.Nil(t, people)
			verified, err := store.VerifyEmail(t.Context(), harness.db, 1, "x@example.test", identityTestNow)
			assert.ErrorIs(t, err, ErrUnavailable)
			assert.False(t, verified)
		})
	}
}

// TestPrincipalStateStoreMatchesTheStatementsItReplaces renders every reader
// through the real PostgreSQL dialect and asserts its exact text. These strings
// are written out again here on purpose: they are the same literals
// internal/iam/authn/repository_identity_test.go froze for the statements authn
// runs today, written independently on both sides, so moving a statement from
// authn to its owner cannot quietly change a byte of SQL. Two of them feed query
// plans pinned in internal/app/testdata/iam_performance_baseline.json, whose
// comparison needs an external database that `make ci` does not provide.
func TestPrincipalStateStoreMatchesTheStatementsItReplaces(t *testing.T) {
	const serviceAccountExclusion = `SELECT 1 FROM sl_iam_service_accounts AS service_account ` +
		`WHERE service_account.principal_id = principal.id`

	harness := newIdentityHarness(t, "sl_")
	connection, err := harness.db.DB()
	require.NoError(t, err)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{
		DryRun: true, SkipDefaultTransaction: true, DisableAutomaticPing: true,
		NowFunc: func() time.Time { return identityTestNow },
		Logger:  logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	var recorded []string
	capture := func(tx *gorm.DB) { recorded = append(recorded, tx.Statement.SQL.String()) }
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("state_query", capture))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("state_update", capture))

	store := principalStateStore("sl_")
	for name, testCase := range map[string]struct {
		want []string
		call func() error
	}{
		"lock": {
			[]string{`UPDATE "sl_iam_principals" SET "updated_at"=updated_at WHERE id = $1`},
			func() error { return store.Lock(t.Context(), db, 101) },
		},
		"lock active": {
			[]string{`UPDATE "sl_iam_principals" SET "updated_at"=updated_at WHERE id = $1 AND status = $2`},
			func() error { return store.LockActive(t.Context(), db, 101) },
		},
		"active row": {
			[]string{`SELECT * FROM "sl_iam_principals" WHERE id = $1 AND status = $2 LIMIT $3`},
			func() error { _, _, err := store.ActiveRow(t.Context(), db, 101); return err },
		},
		"lock active row": {
			[]string{`SELECT * FROM "sl_iam_principals" WHERE id = $1 AND status = $2 LIMIT $3 FOR UPDATE`},
			func() error { _, _, err := store.LockActiveRow(t.Context(), db, 101); return err },
		},
		"lock by login": {
			[]string{`SELECT * FROM "sl_iam_principals" WHERE login_name = $1 LIMIT $2 FOR UPDATE`},
			func() error { _, _, err := store.LockByLogin(t.Context(), db, "alice"); return err },
		},
		"human by login": {
			[]string{
				serviceAccountExclusion,
				`SELECT * FROM sl_iam_principals AS principal WHERE principal.login_name = $1 ` +
					`AND NOT EXISTS (` + serviceAccountExclusion + `) LIMIT $2`,
			},
			func() error { _, _, err := store.HumanByLogin(t.Context(), db, "alice"); return err },
		},
		"active human by identifier": {
			[]string{
				serviceAccountExclusion,
				`SELECT * FROM sl_iam_principals AS principal ` +
					`WHERE (status = $1 AND email_verified = $2) ` +
					`AND (LOWER(principal.login_name) = $3 OR LOWER(principal.email) = $4) ` +
					`AND NOT EXISTS (` + serviceAccountExclusion + `) LIMIT $5`,
			},
			func() error { _, err := store.ActiveHumanByIdentifier(t.Context(), db, "a@b.test"); return err },
		},
		"verify email": {
			[]string{`UPDATE "sl_iam_principals" SET "activation_required"=$1,"email_verified"=$2,` +
				`"updated_at"=$3 WHERE id = $4 AND status = $5 AND email = $6 AND email_verified = $7`},
			func() error {
				_, err := store.VerifyEmail(t.Context(), db, 101, "a@b.test", identityTestNow)
				return err
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorded = nil
			_ = testCase.call()
			assert.Equal(t, testCase.want, recorded)
		})
	}
}

// TestPrincipalStateReadersReportAbsenceAndAmbiguityWithoutDeciding covers the
// two behaviors a caller cannot recover if the capability gets them wrong.
//
// A lock that cannot say whether it locked anything is the dangerous one: the
// callers that guard password recovery, MFA enrollment and email verification
// keep only the error, so a suspended principal that produced no row has to
// come back as found=false, not as a silent success.
//
// Ambiguity is the other: two people sharing an identifier is a policy question,
// so the reader returns both rows and lets the caller decide what it means.
func TestPrincipalStateReadersReportAbsenceAndAmbiguityWithoutDeciding(t *testing.T) {
	harness := newIdentityHarness(t, "pstate_readers_")
	store := (&Module{runtime: harness.runtime}).principalStateStore()
	ctx := t.Context()

	active, err := harness.repository.CreateBootstrapPrincipalOn(ctx, harness.db, BootstrapPrincipalInput{
		LoginName: "alice", DisplayName: "Alice", Email: "shared@example.test",
	}, identityTestNow)
	require.NoError(t, err)
	suspended, err := harness.repository.CreateBootstrapPrincipalOn(ctx, harness.db, BootstrapPrincipalInput{
		LoginName: "mallory", DisplayName: "Mallory", Email: "mallory@example.test",
	}, identityTestNow)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", suspended).Update("status", "disabled").Error)

	t.Run("an absent principal is not found", func(t *testing.T) {
		for name, read := range map[string]func() (Principal, bool, error){
			"active row":      func() (Principal, bool, error) { return store.ActiveRow(ctx, harness.db, 404) },
			"lock active row": func() (Principal, bool, error) { return store.LockActiveRow(ctx, harness.db, 404) },
			"lock by login":   func() (Principal, bool, error) { return store.LockByLogin(ctx, harness.db, "nobody") },
			"human by login":  func() (Principal, bool, error) { return store.HumanByLogin(ctx, harness.db, "nobody") },
		} {
			_, found, err := read()
			require.NoError(t, err, name)
			assert.False(t, found, name)
		}
	})

	t.Run("a suspended principal is not found by the active readers", func(t *testing.T) {
		_, found, err := store.ActiveRow(ctx, harness.db, suspended)
		require.NoError(t, err)
		assert.False(t, found)
		_, found, err = store.LockActiveRow(ctx, harness.db, suspended)
		require.NoError(t, err)
		assert.False(t, found, "a lock that cannot report this lets a suspended principal through")

		// The same row is still reachable without the active predicate, which is
		// what a reconciling caller needs.
		row, found, err := store.LockByLogin(ctx, harness.db, "mallory")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "disabled", row.Status)
	})

	t.Run("an ambiguous identifier comes back as two rows", func(t *testing.T) {
		twin, err := harness.repository.CreateBootstrapPrincipalOn(ctx, harness.db, BootstrapPrincipalInput{
			LoginName: "alice-twin", DisplayName: "Twin", Email: "shared@example.test",
		}, identityTestNow)
		require.NoError(t, err)

		people, err := store.ActiveHumanByIdentifier(ctx, harness.db, "  SHARED@EXAMPLE.TEST  ")
		require.NoError(t, err)
		require.Len(t, people, 2, "deciding what this means is the caller's policy")
		assert.ElementsMatch(t, []guid.ID{active, twin}, []guid.ID{people[0].ID, people[1].ID})

		one, err := store.ActiveHumanByIdentifier(ctx, harness.db, "alice")
		require.NoError(t, err)
		require.Len(t, one, 1)
		assert.Equal(t, active, one[0].ID)

		none, err := store.ActiveHumanByIdentifier(ctx, harness.db, "mallory@example.test")
		require.NoError(t, err)
		assert.Empty(t, none, "a suspended principal is not a recovery candidate")
	})

	t.Run("verify email only moves a pending active row", func(t *testing.T) {
		// A bootstrap principal is created already verified, so this puts the row
		// back into the pending state the verification flow acts on.
		require.NoError(t, harness.db.Table(harness.table("iam_principals")).Where("id = ?", active).
			Updates(map[string]any{"email_verified": false, "activation_required": true}).Error)

		verified, err := store.VerifyEmail(ctx, harness.db, active, "shared@example.test", identityTestNow)
		require.NoError(t, err)
		assert.True(t, verified)

		again, err := store.VerifyEmail(ctx, harness.db, active, "shared@example.test", identityTestNow)
		require.NoError(t, err)
		assert.False(t, again, "a row already verified must report no change")

		onSuspended, err := store.VerifyEmail(ctx, harness.db, suspended, "mallory@example.test", identityTestNow)
		require.NoError(t, err)
		assert.False(t, onSuspended)
	})
}

// TestPrincipalStateReadersReportAnUnreadableTableAsAFailure separates "no such
// principal" from "storage is broken". Every reader must keep those apart: a
// caller that treats a failure as an absence would let an operation proceed as
// though the principal simply was not there.
func TestPrincipalStateReadersReportAnUnreadableTableAsAFailure(t *testing.T) {
	harness := newIdentityHarness(t, "pstate_broken_")
	store := (&Module{runtime: harness.runtime}).principalStateStore()
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_principals")).Error)
	ctx := t.Context()

	for name, read := range map[string]func() (Principal, bool, error){
		"active row":      func() (Principal, bool, error) { return store.ActiveRow(ctx, harness.db, 1) },
		"lock active row": func() (Principal, bool, error) { return store.LockActiveRow(ctx, harness.db, 1) },
		"lock by login":   func() (Principal, bool, error) { return store.LockByLogin(ctx, harness.db, "alice") },
		"human by login":  func() (Principal, bool, error) { return store.HumanByLogin(ctx, harness.db, "alice") },
	} {
		t.Run(name, func(t *testing.T) {
			_, found, err := read()
			require.Error(t, err)
			assert.ErrorContains(t, err, "load principal")
			assert.False(t, found)
		})
	}

	_, err := store.ActiveHumanByIdentifier(ctx, harness.db, "alice")
	require.Error(t, err)
	assert.ErrorContains(t, err, "load principals by identifier")

	verified, err := store.VerifyEmail(ctx, harness.db, 1, "a@example.test", identityTestNow)
	require.Error(t, err)
	assert.ErrorContains(t, err, "verify principal email")
	assert.False(t, verified)
}
