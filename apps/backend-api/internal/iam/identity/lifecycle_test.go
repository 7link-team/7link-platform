package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPrincipalLifecycleRetentionAndTombstone(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "lc_")
	ctx := t.Context()
	p, err := h.service.Create(ctx, 7, "private-login", testPassword, "Private Name", "private@example.test")
	require.NoError(t, err)
	initial, err := h.service.GetPrincipalLifecycle(ctx, p.ID)
	require.NoError(t, err)
	assert.Zero(t, initial.Version)
	assert.Nil(t, initial.RetainUntil)
	assert.Nil(t, initial.UpdatedAt)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, 1, p.LoginName)
	require.ErrorIs(t, err, ErrUnavailable)

	cleanupCalls := 0
	var obligations []audit.ErasureObligation
	h.service.erasureRecorder = func(_ context.Context, obligation audit.ErasureObligation) error {
		obligations = append(obligations, obligation)
		return nil
	}
	h.service.anonymizer = func(ctx context.Context, tx *gorm.DB, current Principal, _ time.Time) error {
		cleanupCalls++
		assert.Equal(t, p.LoginName, current.LoginName)
		assert.Equal(t, p.Email, current.Email)
		return tx.WithContext(ctx).Table(h.table("iam_credentials")).Where("principal_id = ?", current.ID).Delete(map[string]any{}).Error
	}
	past := time.Now().UTC().Add(-time.Hour)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, past, true, "case-123")
	require.NoError(t, err)
	assert.Equal(t, int64(1), policy.Version)
	assert.True(t, policy.LegalHold)
	_, err = h.service.SetPrincipalRetention(ctx, p.ID, 0, past, false, "stale")
	require.ErrorIs(t, err, ErrLifecycleConflict)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.ErrorIs(t, err, ErrSuspensionRequired)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.ErrorIs(t, err, ErrPrincipalRetained)
	assert.Zero(t, cleanupCalls)

	policy, err = h.service.SetPrincipalRetention(ctx, p.ID, policy.Version, time.Now().Add(time.Hour), false, "case-123")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.ErrorIs(t, err, ErrPrincipalRetained)
	policy, err = h.service.SetPrincipalRetention(ctx, p.ID, policy.Version, past, false, "case-123")
	require.NoError(t, err)
	erased, err := h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.NoError(t, err)
	require.NotNil(t, erased.AnonymizedAt)
	assert.Empty(t, erased.Reference)
	assert.Equal(t, policy.Version+1, erased.Version)
	assert.Equal(t, 1, cleanupCalls)
	require.Len(t, obligations, 1)
	assert.Equal(t, p.ID, obligations[0].PrincipalID)
	assert.Equal(t, erased.Version, obligations[0].LifecycleVersion)
	row := h.principal(t, p.ID)
	assert.Equal(t, "erased:"+p.ID.String(), row.LoginName)
	assert.Equal(t, "Anonymized account", row.DisplayName)
	assert.Empty(t, row.Email)
	assert.Equal(t, "disabled", row.Status)
	assert.False(t, row.EmailVerified)
	assert.False(t, row.ActivationRequired)
	assert.Equal(t, int64(1), h.principalCount(t))
	assert.Zero(t, h.credentialVersion(t, p.ID))

	repeated, err := h.service.AnonymizePrincipal(ctx, p.ID, erased.Version, p.LoginName)
	require.NoError(t, err)
	assert.Equal(t, erased, repeated)
	assert.Equal(t, 1, cleanupCalls)
	require.Len(t, obligations, 2, "an existing tombstone must backfill or re-verify its external obligation")
	assert.Equal(t, obligations[0].PrincipalID, obligations[1].PrincipalID)
	assert.Equal(t, obligations[0].LifecycleVersion, obligations[1].LifecycleVersion)
	_, err = h.service.SetPrincipalRetention(ctx, p.ID, erased.Version, past, false, "new-case")
	require.ErrorIs(t, err, ErrPrincipalAnonymized)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "active")
	require.Error(t, err)
	name := "Restore PII"
	_, err = h.service.Update(ctx, 7, p.ID, &name, nil)
	require.Error(t, err)
	require.Error(t, h.db.Table(h.table("iam_principals")).Where("id = ?", p.ID).Update("email", "repopulated@example.test").Error)
	require.Error(t, h.db.Table(h.table("iam_principals")).Where("id = ?", p.ID).Delete(map[string]any{}).Error)
	require.Error(t, h.db.Table(h.table("iam_principal_lifecycles")).Where("principal_id = ?", p.ID).Update("anonymized_at", nil).Error)
	require.Error(t, h.db.Table(h.table("iam_principal_lifecycles")).Where("principal_id = ?", p.ID).Delete(map[string]any{}).Error)
	_, err = h.service.ReplaceProvisionedTo(ctx, h.db, 7, p.ID, ProvisionedPrincipalInput{
		LoginName: "restored", DisplayName: name, Email: "repopulated@example.test", Active: false,
	})
	require.Error(t, err)
	events := h.events()
	assert.Equal(t, "principal_anonymized", events[len(events)-1].EventType)
	assert.Zero(t, events[len(events)-1].TenantID)
	assert.NotContains(t, events[len(events)-1].Detail, "email")
}

func TestPrincipalLifecycleRollback(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"cleanup", "ledger", "audit"} {
		t.Run(failure, func(t *testing.T) {
			h := newIdentityHarness(t, "")
			ctx := t.Context()
			p, err := h.service.Create(ctx, 7, "rollback", testPassword, "Keep Name", "keep@example.test")
			require.NoError(t, err)
			_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
			require.NoError(t, err)
			policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().Add(-time.Hour), false, "case-rollback")
			require.NoError(t, err)
			sentinel := errors.New("injected " + failure + " failure")
			h.service.anonymizer = func(ctx context.Context, tx *gorm.DB, current Principal, _ time.Time) error {
				if err := tx.WithContext(ctx).Table(h.table("iam_credentials")).Where("principal_id = ?", current.ID).Delete(map[string]any{}).Error; err != nil {
					return err
				}
				if failure == "cleanup" {
					return sentinel
				}
				return nil
			}
			if failure == "audit" {
				h.auditErr = sentinel
			}
			if failure == "ledger" {
				h.service.erasureRecorder = func(context.Context, audit.ErasureObligation) error { return sentinel }
			}
			_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
			require.ErrorIs(t, err, sentinel)
			row := h.principal(t, p.ID)
			assert.Equal(t, p.Email, row.Email)
			assert.Equal(t, p.LoginName, row.LoginName)
			assert.Equal(t, int64(1), h.credentialVersion(t, p.ID))
			current, err := h.service.GetPrincipalLifecycle(ctx, p.ID)
			require.NoError(t, err)
			assert.Equal(t, policy, current)
		})
	}
}

func TestPrincipalLifecycleValidation(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "")
	ctx := t.Context()
	_, err := h.service.GetPrincipalLifecycle(ctx, 0)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.GetPrincipalLifecycle(ctx, 99)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.SetPrincipalRetention(ctx, 99, 0, time.Time{}, false, "case")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.SetPrincipalRetention(ctx, 99, 0, time.Now(), false, " ")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.SetPrincipalRetention(ctx, 99, 0, time.Now(), false, "case")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.AnonymizePrincipal(ctx, 99, 0, "absent")
	require.ErrorIs(t, err, ErrInvalid)
	h.seedTenant(t, 7, "active")
	account, err := h.service.CreateServiceAccount(ctx, 7, "automation", "Automation", "", nil)
	require.NoError(t, err)
	_, err = h.service.GetPrincipalLifecycle(ctx, account.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestPrincipalLifecycleStatusNamesWhatToGoAndDo covers the projection the
// console renders. Each blocker is a thing an operator can act on, and reading
// the status must never itself record a policy -- looking at a principal cannot
// be what starts its retention clock.
func TestPrincipalLifecycleStatusNamesWhatToGoAndDo(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	p, err := h.service.Create(ctx, 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)

	status, err := h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.LoginName, status.LoginName)
	assert.False(t, status.Suspended)
	assert.False(t, status.Anonymized)
	assert.ElementsMatch(t, []string{LifecycleBlockerNotSuspended, LifecycleBlockerNoRetention}, status.Blockers)
	assert.Zero(t, status.Lifecycle.Version, "reading must not have recorded a policy")

	future := time.Now().UTC().Add(48 * time.Hour)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, future, true, "case-1")
	require.NoError(t, err)

	status, err = h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{LifecycleBlockerNotSuspended, LifecycleBlockerRetentionActive, LifecycleBlockerLegalHold},
		status.Blockers)
	assert.Equal(t, policy.Version, status.Lifecycle.Version)
	assert.True(t, status.Lifecycle.LegalHold)

	past := time.Now().UTC().Add(-time.Hour)
	_, err = h.service.SetPrincipalRetention(ctx, p.ID, policy.Version, past, false, "case-1")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)

	status, err = h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, status.Blockers, "with nothing left to do the operator is told so")
	assert.True(t, status.Suspended)
}

// TestAnonymizePrincipalRequiresNamingTheSubject covers the confirmation. It is
// checked under the row lock, so a caller cannot satisfy it with a login name
// read before someone else renamed the principal.
func TestAnonymizePrincipalRequiresNamingTheSubject(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error { return nil }
	p, err := h.service.Create(ctx, 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().UTC().Add(-time.Hour), false, "case-1")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)

	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, "someone-else")
	require.ErrorIs(t, err, ErrConfirmationMismatch)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, "")
	require.ErrorIs(t, err, ErrConfirmationMismatch)

	status, err := h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.False(t, status.Anonymized, "a refused confirmation must not have erased anything")

	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, "  "+p.LoginName+"  ")
	require.NoError(t, err, "surrounding whitespace is a typing artifact, not a different subject")
}

// conflictError stands in for what a driver returns when the database has
// already rolled the transaction back to break a lock cycle.
type conflictError struct{ state string }

func (e conflictError) Error() string    { return "deadlock detected" }
func (e conflictError) SQLState() string { return e.state }

// TestAnonymizePrincipalRetriesAfterTheDatabaseBreaksALockCycle covers the case
// live traffic creates: erasure holds the principal and reaches for an owner's
// rows while an ordinary request holds those rows and reaches for the principal.
// The database rolls one side back, and which side is not ours to choose -- so
// erasure losing must not turn into an operator seeing a raw deadlock.
func TestAnonymizePrincipalRetriesAfterTheDatabaseBreaksALockCycle(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	attempts := 0
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("erase oauth principal data: %w", conflictError{state: "40P01"})
		}
		return nil
	}
	p, err := h.service.Create(ctx, 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().UTC().Add(-time.Hour), false, "case-1")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)

	erased, err := h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "the rolled-back attempt changed nothing, so it is run again")
	assert.NotNil(t, erased.AnonymizedAt)

	status, err := h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, status.Anonymized, "the retry committed the erasure exactly once")
	assert.Equal(t, policy.Version+1, status.Lifecycle.Version)
}

func TestAnonymizePrincipalStopsRetryingAConflictThatKeepsRecurring(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	attempts := 0
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error {
		attempts++
		return fmt.Errorf("erase oauth principal data: %w", conflictError{state: "40P01"})
	}
	p, err := h.service.Create(ctx, 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().UTC().Add(-time.Hour), false, "case-1")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)

	// Losing every time is a different problem from losing once, and it has to
	// surface rather than spin.
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.Error(t, err)
	assert.Equal(t, anonymizeAttempts, attempts)

	status, err := h.service.PrincipalLifecycleStatus(ctx, p.ID)
	require.NoError(t, err)
	assert.False(t, status.Anonymized, "nothing was erased")
}

// TestAnonymizePrincipalDoesNotRetryARefusal keeps the retry aimed at
// contention only. A fence is a decision about this principal; repeating it
// would reach the same answer and hide how long the caller waited for it.
func TestAnonymizePrincipalDoesNotRetryARefusal(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	attempts := 0
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error {
		attempts++
		return nil
	}
	p, err := h.service.Create(ctx, 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().UTC().Add(72*time.Hour), false, "case-1")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)

	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.ErrorIs(t, err, ErrPrincipalRetained)
	assert.Zero(t, attempts, "the retention fence is reached before any owner is touched")
}
