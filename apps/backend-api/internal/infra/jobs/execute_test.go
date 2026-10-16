package jobs

import (
	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/jobs/model"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInvokeHandlerPreservesResultAndJobIdentity(t *testing.T) {
	failure := errors.New("handler failed")
	job := Job{ID: 7, TenantID: 2, Kind: "jobs.work", Attempt: 3, MaxAttempts: 8}
	handler := Handler{Run: func(ctx context.Context, actual Job) error {
		require.Equal(t, job, actual)
		require.NoError(t, ctx.Err())
		return failure
	}}
	require.ErrorIs(t, invokeHandler(t.Context(), handler, job), failure)
	handler.Run = func(context.Context, Job) error { return nil }
	require.NoError(t, invokeHandler(t.Context(), handler, job))
}

func TestInvokeHandlerPanicDoesNotEscapeOrPersistPanicValue(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	handler := m.handlers[claimed.Kind]
	handler.Run = func(context.Context, Job) error { panic("password=private-token") }
	failure := invokeHandler(t.Context(), handler, Job{ID: claimed.ID, Kind: claimed.Kind})
	require.ErrorIs(t, failure, errHandlerPanic)
	require.NotContains(t, failure.Error(), "private-token")
	changed, err := m.finish(t.Context(), claimed, handler, failure, nil)
	require.NoError(t, err)
	require.True(t, changed)
	row := storedJob(t, db, claimed.ID)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, "PANIC", row.LastErrorCode)
	require.NotContains(t, row.LastError, "private-token")
	require.NotContains(t, row.ErrorHistory, "private-token")
	// Modern Go converts panic(nil) into a non-nil runtime panic value.
	handler.Run = func(context.Context, Job) error { panic(nil) }
	require.ErrorIs(t, invokeHandler(t.Context(), handler, Job{}), errHandlerPanic)
}

func TestInvokeHandlerCancellationCannotBecomeSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, invokeHandler(ctx, Handler{Run: func(context.Context, Job) error { t.Fatal("canceled invocation started"); return nil }}, Job{}), context.Canceled)
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := invokeHandler(ctx, Handler{Run: func(ctx context.Context, _ Job) error { <-ctx.Done(); return nil }}, Job{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestInvokeHandlerDoesNotReturnWhileCanceledHandlerStillRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	handler := Handler{Run: func(context.Context, Job) error { close(entered); <-release; return nil }}
	go func() { result <- invokeHandler(ctx, handler, Job{}) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-result:
		t.Fatal("invocation returned while handler was still running")
	default:
	}
	close(release)
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish")
	}
}

type executionRecorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *executionRecorder) Record(_ context.Context, event audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func TestExecuteClaimPersistsOutcomeAndSafeAudit(t *testing.T) {
	for _, failing := range []bool{false, true} {
		t.Run(fmt.Sprint(failing), func(t *testing.T) {
			m, db := queueHarness(t, 10)
			claim := runningJob(t, m)
			recorder := &executionRecorder{}
			m.recorder = recorder
			handler := m.handlers[claim.Kind]
			handler.Run = func(_ context.Context, job Job) error {
				require.Equal(t, claim.ID, job.ID)
				require.Equal(t, claim.Attempts, job.Attempt)
				if failing {
					return RetryAfter(time.Minute, errors.New("password=private-token"))
				}
				return nil
			}
			m.handlers[claim.Kind] = handler
			changed, err := m.executeClaim(t.Context(), claim, nil)
			require.NoError(t, err)
			require.True(t, changed)
			row := storedJob(t, db, claim.ID)
			require.Len(t, recorder.events, 1)
			event := recorder.events[0]
			require.Equal(t, audit.KindJob, event.Entry)
			require.Equal(t, claim.Kind, event.Operation)
			require.Equal(t, "background_job", event.ResourceType)
			require.Equal(t, claim.ID.String(), event.ResourceID)
			require.Equal(t, "1", event.Metadata["attempt"])
			require.NotContains(t, event.Error, "private-token")
			if failing {
				require.Equal(t, "PENDING", row.State)
				require.Equal(t, audit.OutcomeFailure, event.Outcome)
				require.GreaterOrEqual(t, row.RunAtMS, row.UpdatedAt.UnixMilli()+60000)
			} else {
				require.Equal(t, "SUCCEEDED", row.State)
				require.Equal(t, audit.OutcomeSuccess, event.Outcome)
			}
		})
	}
}

func TestExecuteClaimConcurrencyBusyDoesNotInvoke(t *testing.T) {
	m, db := queueHarness(t, 10)
	claim := runningJob(t, m)
	key := "shared"
	claim.ConcurrencyKey = &key
	lock, err := m.tryAcquire(t.Context(), concurrencyLockName(claim.Kind, key), time.Second, func(string, error) {})
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Unlock(context.Background())) }()
	h := m.handlers[claim.Kind]
	h.Run = func(context.Context, Job) error { t.Fatal("occupied key executed"); return nil }
	m.handlers[claim.Kind] = h
	changed, err := m.executeClaim(t.Context(), claim, nil)
	require.NoError(t, err)
	require.True(t, changed)
	row := storedJob(t, db, claim.ID)
	require.Equal(t, "PENDING", row.State)
	require.Zero(t, row.Attempts)
	require.Equal(t, row.UpdatedAt.UnixMilli()+5000, row.RunAtMS)
	require.Empty(t, m.recorder.(*executionRecorder).events)
}

func TestExecuteClaimRenewsAndDiscardsReplacedLease(t *testing.T) {
	m, db := queueHarness(t, 10)
	m.config.Lease = 90 * time.Millisecond
	claim := runningJob(t, m)
	h := m.handlers[claim.Kind]
	h.Run = func(ctx context.Context, _ Job) error {
		require.Eventually(t, func() bool {
			return *storedJob(t, db, claim.ID).LeaseExpiresAtMS > *claim.LeaseExpiresAtMS+30
		}, 3*time.Second, 10*time.Millisecond)
		require.NoError(t, db.Table("j_background_jobs").Where("id = ?", claim.ID).Update("lease_token", claim.LeaseToken+1).Error)
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
			t.Error("lease replacement did not cancel handler")
		}
		return nil
	}
	m.handlers[claim.Kind] = h
	changed, err := m.executeClaim(t.Context(), claim, nil)
	require.ErrorIs(t, err, errExecutionLeaseLost)
	require.False(t, changed)
	row := storedJob(t, db, claim.ID)
	require.Equal(t, "RUNNING", row.State)
	require.Equal(t, claim.LeaseToken+1, row.LeaseToken)
	require.Nil(t, row.FinishedAt)
}

func TestExecuteClaimTimeoutAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			m, db := queueHarness(t, 10)
			claim := runningJob(t, m)
			claim.TimeoutMS = 20
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h := m.handlers[claim.Kind]
			h.Run = func(ctx context.Context, _ Job) error {
				if shutdown {
					cancel()
				}
				<-ctx.Done()
				return nil
			}
			m.handlers[claim.Kind] = h
			changed, err := m.executeClaim(ctx, claim, nil)
			require.NoError(t, err)
			require.True(t, changed)
			row := storedJob(t, db, claim.ID)
			require.Equal(t, "PENDING", row.State)
			if shutdown {
				require.Equal(t, "INTERRUPTED", row.LastErrorCode)
				require.Zero(t, row.Attempts)
			} else {
				require.Equal(t, "TIMEOUT", row.LastErrorCode)
				require.Equal(t, 1, row.Attempts)
			}
		})
	}
}

func TestConcurrencyLockNamesBoundedAndDistinct(t *testing.T) {
	require.Equal(t, "jobs:ck:jobs.work:one", concurrencyLockName("jobs.work", "one"))
	first := concurrencyLockName(strings.Repeat("a", 128), strings.Repeat("界", 128))
	require.LessOrEqual(t, len(first), 191)
	require.Equal(t, first, concurrencyLockName(strings.Repeat("a", 128), strings.Repeat("界", 128)))
	require.NotEqual(t, first, concurrencyLockName(strings.Repeat("a", 127), "a"+strings.Repeat("界", 128)))
}

func TestExecuteClaimConcurrencyLossCancelsAndReleases(t *testing.T) {
	m, db := queueHarness(t, 10)
	claim := runningJob(t, m)
	key := "shared"
	claim.ConcurrencyKey = &key
	original := m.tryAcquire
	var lose func(string, error)
	m.tryAcquire = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
		lose = onLost
		return original(ctx, name, ttl, onLost)
	}
	h := m.handlers[claim.Kind]
	h.Run = func(ctx context.Context, _ Job) error {
		lose("shared", errors.New("lost"))
		<-ctx.Done()
		return nil
	}
	m.handlers[claim.Kind] = h
	changed, err := m.executeClaim(t.Context(), claim, nil)
	require.ErrorIs(t, err, errExecutionLeaseLost)
	require.False(t, changed)
	require.Equal(t, "RUNNING", storedJob(t, db, claim.ID).State)
	lock, err := original(t.Context(), concurrencyLockName(claim.Kind, key), time.Second, func(string, error) {})
	require.NoError(t, err)
	require.NoError(t, lock.Unlock(t.Context()))
}

func TestExecuteClaimRejectsInvalidStaleAndUnavailable(t *testing.T) {
	m, db := queueHarness(t, 10)
	claim := runningJob(t, m)
	_, err := m.executeClaim(t.Context(), model.Job{}, nil)
	require.ErrorIs(t, err, ErrInvalidRequest)
	invalid := claim
	invalid.TimeoutMS = math.MaxInt64
	_, err = m.executeClaim(t.Context(), invalid, nil)
	require.ErrorIs(t, err, ErrInvalidRequest)
	invalid = claim
	invalid.Kind = "missing.kind"
	_, err = m.executeClaim(t.Context(), invalid, nil)
	require.ErrorIs(t, err, ErrUnknownKind)
	recorder := m.recorder
	m.recorder = nil
	_, err = m.executeClaim(t.Context(), claim, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	m.recorder = recorder
	require.NoError(t, db.Table("j_background_jobs").Where("id = ?", claim.ID).Update("lease_token", claim.LeaseToken+1).Error)
	changed, err := m.executeClaim(t.Context(), claim, nil)
	require.ErrorIs(t, err, errExecutionLeaseLost)
	require.False(t, changed)
	require.Empty(t, recorder.(*executionRecorder).events)
}

func TestExecuteClaimAcquisitionFailureDoesNotRunOrFinish(t *testing.T) {
	for _, acquireError := range []error{nil, errors.New("lock unavailable")} {
		m, db := queueHarness(t, 10)
		claim := runningJob(t, m)
		key := "one"
		claim.ConcurrencyKey = &key
		m.tryAcquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
			return nil, acquireError
		}
		changed, err := m.executeClaim(t.Context(), claim, nil)
		if acquireError != nil {
			require.ErrorIs(t, err, acquireError)
		} else {
			require.ErrorIs(t, err, ErrUnavailable)
		}
		require.False(t, changed)
		require.Equal(t, "RUNNING", storedJob(t, db, claim.ID).State)
		require.Empty(t, m.recorder.(*executionRecorder).events)
	}
}
