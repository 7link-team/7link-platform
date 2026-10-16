package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClaimOrderingEligibilityAndLease(t *testing.T) {
	m, db := queueHarness(t, 100)
	reqs := []EnqueueRequest{
		{TenantID: 1, Kind: "jobs.work", Priority: 7},
		{TenantID: 1, Kind: "jobs.work", Priority: 1},
		{TenantID: 1, Kind: "jobs.work", Priority: 3, RunAt: time.Now().Add(time.Hour)},
		{TenantID: 1, Kind: "jobs.work", Priority: 2},
		{TenantID: 1, Kind: "jobs.other", Priority: 1},
	}
	queued, err := m.EnqueueBatch(t.Context(), nil, reqs)
	require.NoError(t, err)
	// A disabled module's queued task remains PENDING and does not consume slots.
	m.mu.Lock()
	delete(m.handlers, "jobs.other")
	m.mu.Unlock()
	claimed, err := m.claim(t.Context(), "default", "replica-a", 2)
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	require.Equal(t, queued[1].JobID, claimed[0].ID)
	require.Equal(t, queued[3].JobID, claimed[1].ID)
	for _, row := range claimed {
		require.Equal(t, "RUNNING", row.State)
		require.Equal(t, 1, row.Attempts)
		require.EqualValues(t, 1, row.LeaseToken)
		require.Equal(t, 2, row.Version)
		require.Equal(t, "replica-a", *row.LeaseOwner)
		require.NotNil(t, row.StartedAt)
		require.Equal(t, row.StartedAt.UnixMilli()+60000, *row.LeaseExpiresAtMS)
		var persisted model.Job
		require.NoError(t, db.Table("j_background_jobs").Where("id=?", row.ID).Take(&persisted).Error)
		require.Equal(t, row.Attempts, persisted.Attempts)
		require.Equal(t, row.LeaseToken, persisted.LeaseToken)
	}
	claimed, err = m.claim(t.Context(), "default", "replica-b", 5)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, queued[0].JobID, claimed[0].ID)
	var pending int64
	require.NoError(t, db.Table("j_background_jobs").Where("state=?", "PENDING").Count(&pending).Error)
	require.EqualValues(t, 2, pending)
}

func TestClaimTenantCapPlatformExemptionAndConcurrentOwnership(t *testing.T) {
	m, db := queueHarness(t, 100)
	m.config.MaxRunningPerTenant = 2
	var reqs []EnqueueRequest
	for _, tenant := range []guid.ID{1, 2, 0} {
		for range 5 {
			reqs = append(reqs, EnqueueRequest{TenantID: tenant, Kind: "jobs.work"})
		}
	}
	_, err := m.EnqueueBatch(t.Context(), nil, reqs)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan []model.Job, 4)
	errs := make(chan error, 4)
	for _, owner := range []string{"a", "b", "c", "d"} {
		wg.Go(func() { rows, err := m.claim(t.Context(), "default", owner, 4); results <- rows; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[guid.ID]bool{}
	counts := map[guid.ID]int{}
	for rows := range results {
		for _, row := range rows {
			require.False(t, seen[row.ID], "same pending job claimed twice")
			seen[row.ID] = true
			counts[row.TenantID]++
		}
	}
	require.Equal(t, 2, counts[1])
	require.Equal(t, 2, counts[2])
	require.Equal(t, 5, counts[0], "platform tasks are exempt from tenant concurrency cap")
	var running int64
	require.NoError(t, db.Table("j_background_jobs").Where("state=?", "RUNNING").Count(&running).Error)
	require.EqualValues(t, 9, running)
	empty, err := m.claim(t.Context(), "default", "e", 4)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestClaimVersionAndTokenAdvanceOnReclaim(t *testing.T) {
	m, db := queueHarness(t, 10)
	_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.NoError(t, err)
	first, err := m.claim(t.Context(), "default", "first", 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", first[0].ID).Update("state", "PENDING").Error)
	second, err := m.claim(t.Context(), "default", "second", 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, first[0].ID, second[0].ID)
	require.EqualValues(t, 2, second[0].LeaseToken)
	require.Equal(t, 2, second[0].Attempts)
	require.Equal(t, 3, second[0].Version)
}

func TestClaimValidationAndFailure(t *testing.T) {
	m, _ := queueHarness(t, 10)
	for _, tc := range []struct {
		queue, owner string
		slots        int
	}{{"bad-name", "a", 1}, {"default", "", 1}, {"default", strings.Repeat("x", 65), 1}, {"default", "a", -1}, {"default", string([]byte{0xff}), 1}} {
		_, err := m.claim(t.Context(), tc.queue, tc.owner, tc.slots)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
	rows, err := m.claim(t.Context(), "default", "a", 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = m.claim(t.Context(), "unregistered", "a", 1)
	require.NoError(t, err)
	require.Empty(t, rows)
	m.sealed = false
	_, err = m.claim(t.Context(), "default", "a", 1)
	require.ErrorIs(t, err, ErrUnavailable)
	m.sealed = true
	_, err = (*Module)(nil).claim(t.Context(), "default", "a", 1)
	require.ErrorIs(t, err, ErrUnavailable)
	failure := errors.New("claim guard unavailable")
	m.guard = func(context.Context, *gorm.DB, string) error { return failure }
	_, err = m.claim(t.Context(), "default", "a", 1)
	require.ErrorIs(t, err, failure)
}
