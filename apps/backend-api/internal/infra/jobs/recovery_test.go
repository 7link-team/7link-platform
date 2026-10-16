package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"7link.com/platform/internal/infra/jobs/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShutdownAndContentionRefundAttemptWithoutDeadLetter(t *testing.T) {
	for _, reason := range []error{errInterrupted, errConcurrencyBusy} {
		t.Run(reason.Error(), func(t *testing.T) {
			m, db := queueHarness(t, 10)
			claimed := runningJob(t, m)
			require.NoError(t, db.Table("j_background_jobs").Where("id=?", claimed.ID).Update("max_attempts", 1).Error)
			changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], reason, nil)
			require.NoError(t, err)
			require.True(t, changed)
			row := storedJob(t, db, claimed.ID)
			require.Equal(t, "PENDING", row.State)
			require.Zero(t, row.Attempts)
			require.Nil(t, row.FinishedAt)
			require.Nil(t, row.LeaseOwner)
			require.Nil(t, row.LeaseExpiresAtMS)
			if errors.Is(reason, errInterrupted) {
				require.Equal(t, row.UpdatedAt.UnixMilli(), row.RunAtMS)
				require.Equal(t, "INTERRUPTED", row.LastErrorCode)
				var history []failureRecord
				require.NoError(t, json.Unmarshal([]byte(row.ErrorHistory), &history))
				require.Len(t, history, 1)
				require.Equal(t, 1, history[0].Attempt)
			} else {
				require.Equal(t, row.UpdatedAt.UnixMilli()+5000, row.RunAtMS)
				require.Empty(t, row.LastErrorCode)
				require.JSONEq(t, "[]", row.ErrorHistory)
			}
			changed, err = m.finish(t.Context(), claimed, m.handlers[claimed.Kind], reason, nil)
			require.NoError(t, err)
			require.False(t, changed)
			require.Zero(t, storedJob(t, db, claimed.ID).Attempts, "duplicate cleanup cannot refund twice")
		})
	}
}

func TestExpiryRechecksRenewalAndReplacement(t *testing.T) {
	m, db := queueHarness(t, 10)
	old := runningJob(t, m)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", old.ID).Update("lease_expires_at_ms", 0).Error)
	// The reaper's scanned snapshot is now stale after a heartbeat.
	renewed, err := m.heartbeat(t.Context(), old)
	require.NoError(t, err)
	require.True(t, renewed)
	changed, err := m.finish(t.Context(), old, m.handlers[old.Kind], errLeaseExpired, nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "RUNNING", storedJob(t, db, old.ID).State)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", old.ID).Update("lease_expires_at_ms", 0).Error)
	changed, err = m.finish(t.Context(), old, m.handlers[old.Kind], errLeaseExpired, nil)
	require.NoError(t, err)
	require.True(t, changed)
	row := storedJob(t, db, old.ID)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, 1, row.Attempts)
	require.Equal(t, "LEASE_EXPIRED", row.LastErrorCode)
	require.GreaterOrEqual(t, row.RunAtMS-row.UpdatedAt.UnixMilli(), int64(2500))
	require.LessOrEqual(t, row.RunAtMS-row.UpdatedAt.UnixMilli(), int64(5000))
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", old.ID).Update("run_at_ms", 0).Error)
	rows, err := m.claim(t.Context(), "default", "new", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	changed, err = m.finish(t.Context(), old, m.handlers[old.Kind], errLeaseExpired, nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, rows[0].LeaseToken, storedJob(t, db, old.ID).LeaseToken)
}

func TestReaperBatchBoundAndConcurrentRecovery(t *testing.T) {
	m, db := queueHarness(t, 200)
	requests := make([]EnqueueRequest, 102)
	for i := range requests {
		requests[i] = EnqueueRequest{Kind: "jobs.work"}
	}
	_, err := m.EnqueueBatch(t.Context(), nil, requests)
	require.NoError(t, err)
	rows, err := m.claim(t.Context(), "default", "worker", 102)
	require.NoError(t, err)
	require.Len(t, rows, 102)
	require.NoError(t, db.Table("j_background_jobs").Where("state=?", "RUNNING").Update("lease_expires_at_ms", 0).Error)
	recovered, err := m.reapExpired(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 100, recovered)
	var running int64
	require.NoError(t, db.Table("j_background_jobs").Where("state=?", "RUNNING").Count(&running).Error)
	require.EqualValues(t, 2, running)
	var wg sync.WaitGroup
	counts := make(chan int, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { n, err := m.reapExpired(t.Context(), nil); counts <- n; errs <- err })
	}
	wg.Wait()
	close(counts)
	close(errs)
	total := 0
	for n := range counts {
		total += n
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 2, total)
	var remaining []model.Job
	require.NoError(t, db.Table("j_background_jobs").Find(&remaining).Error)
	for _, row := range remaining {
		require.Equal(t, "PENDING", row.State)
		require.Equal(t, 1, row.Attempts)
		var history []failureRecord
		require.NoError(t, json.Unmarshal([]byte(row.ErrorHistory), &history))
		require.Len(t, history, 1)
	}
	recovered, err = m.reapExpired(t.Context(), nil)
	require.NoError(t, err)
	require.Zero(t, recovered)
}

func TestReaperDisabledHandlerAndIsolatedPublicationFailure(t *testing.T) {
	m, db := queueHarness(t, 10)
	first := runningJob(t, m)
	second := runningJob(t, m)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", first.ID).Update("max_attempts", 1).Error)
	require.NoError(t, db.Table("j_background_jobs").Where("state=?", "RUNNING").Update("lease_expires_at_ms", 0).Error)
	m.mu.Lock()
	delete(m.handlers, "jobs.work")
	m.mu.Unlock()
	failure := errors.New("outbox unavailable")
	recovered, err := m.reapExpired(t.Context(), func(context.Context, *gorm.DB, DeadLetter) error { return failure })
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, recovered, "one failed dead letter cannot block another task's recovery")
	require.Equal(t, "RUNNING", storedJob(t, db, first.ID).State)
	require.Equal(t, "PENDING", storedJob(t, db, second.ID).State)
	calls := 0
	recovered, err = m.reapExpired(t.Context(), func(_ context.Context, _ *gorm.DB, event DeadLetter) error {
		calls++
		require.Equal(t, first.ID, event.JobID)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, 1, calls)
	require.Equal(t, "DEAD", storedJob(t, db, first.ID).State)
	rows, err := m.claim(t.Context(), "default", "worker", 1)
	require.NoError(t, err)
	require.Empty(t, rows, "disabled kind stays pending")
}

func TestRecoveryRejectsInvalidAttemptAndUnavailableDatabase(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", claimed.ID).Update("attempts", 0).Error)
	changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], errInterrupted, nil)
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.False(t, changed)
	require.Equal(t, "RUNNING", storedJob(t, db, claimed.ID).State)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.reapExpired(ctx, nil)
	require.Error(t, err)
	_, err = (*Module)(nil).reapExpired(t.Context(), nil)
	require.ErrorIs(t, err, ErrUnavailable)
}
