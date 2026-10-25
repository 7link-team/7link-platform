package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func workerHarness(t *testing.T, slots int) (*Module, *gorm.DB) {
	t.Helper()
	m, db := queueHarness(t, 100)
	m.config.Queues = map[string]int{"default": slots}
	m.config.PollInterval = 10 * time.Millisecond
	m.config.DrainTimeout = 200 * time.Millisecond
	m.onDead = func(context.Context, *gorm.DB, DeadLetter) error { return nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, m.Stop(ctx))
	})
	return m, db
}

func TestWorkersRunQueuedJobsWithinSlotsAndStopClaiming(t *testing.T) {
	m, db := workerHarness(t, 2)
	barrier, release := context.WithCancel(t.Context())
	defer release()
	var active, peak atomic.Int32
	var mu sync.Mutex
	calls := map[string]int{}
	h := m.handlers["jobs.work"]
	h.Run = func(ctx context.Context, job Job) error {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		mu.Lock()
		calls[job.ID.String()]++
		mu.Unlock()
		// SQLite ownership checks may serialize before handlers enter. Keep
		// both slots occupied until the test observes their overlap.
		select {
		case <-barrier.Done():
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.handlers[h.Kind] = h
	reqs := make([]EnqueueRequest, 8)
	for i := range reqs {
		reqs[i] = EnqueueRequest{TenantID: 1, Kind: h.Kind}
	}
	rows, err := m.EnqueueBatch(t.Context(), nil, reqs)
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	firstRun := m.workers
	require.NoError(t, m.Start(t.Context()))
	require.Same(t, firstRun, m.workers)
	require.Eventually(t, func() bool { return active.Load() == 2 }, 3*time.Second, time.Millisecond,
		"both worker slots must be occupied before releasing handlers")
	release()
	require.Eventually(t, func() bool {
		for _, row := range rows {
			if storedJob(t, db, row.JobID).State != "SUCCEEDED" {
				return false
			}
		}
		return true
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Quiesce(t.Context()))
	require.EqualValues(t, 2, peak.Load())
	mu.Lock()
	require.Len(t, calls, 8)
	for _, count := range calls {
		require.Equal(t, 1, count)
	}
	mu.Unlock()
	result, err := m.Enqueue(t.Context(), nil, reqs[0])
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context())) // A quiesced lifecycle cannot spawn another pool.
	require.Equal(t, "PENDING", storedJob(t, db, result.JobID).State)
	select {
	case <-m.workers.done:
	default:
		t.Fatal("workers have not drained")
	}
}

func TestWorkersGracefulDrainWaitsForSuccess(t *testing.T) {
	m, db := workerHarness(t, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	h := m.handlers["jobs.work"]
	h.Run = func(ctx context.Context, _ Job) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.handlers[h.Kind] = h
	row, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: h.Kind})
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}
	drained := make(chan error, 1)
	go func() { drained <- m.Quiesce(t.Context()) }()
	select {
	case <-drained:
		t.Fatal("drain returned while task running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-drained)
	require.Equal(t, "SUCCEEDED", storedJob(t, db, row.JobID).State)
}

func TestWorkersBoundedDrainRefundsIgnoredCancellation(t *testing.T) {
	m, db := workerHarness(t, 1)
	m.config.DrainTimeout = 30 * time.Millisecond
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	h := m.handlers["jobs.work"]
	h.Run = func(context.Context, Job) error { close(entered); <-release; return nil }
	m.handlers[h.Kind] = h
	row, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: h.Kind})
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, m.Quiesce(ctx))
	got := storedJob(t, db, row.JobID)
	require.Equal(t, "PENDING", got.State)
	require.Zero(t, got.Attempts)
	require.Equal(t, "INTERRUPTED", got.LastErrorCode)
	require.NoError(t, m.Stop(ctx))
	require.Equal(t, got.Version, storedJob(t, db, row.JobID).Version) // no double refund
	select {
	case <-m.workers.done:
		t.Fatal("ignored-cancellation worker released its slot")
	default:
	}
}

func TestWorkerConfigurationAndDeadLetterBinding(t *testing.T) {
	m := catalogModule()
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[DeadLetterConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidRequest)
	appendEvent := func(context.Context, *gorm.DB, DeadLetter) error { return nil }
	require.NoError(t, connector.Bind(appendEvent))
	require.ErrorIs(t, connector.Bind(appendEvent), ErrInvalidRequest)
	require.NoError(t, m.Initialize(t.Context()))
	require.ErrorIs(t, connector.Bind(appendEvent), ErrCatalogSealed)
	require.ErrorIs(t, NewModule(nil, Config{}).Start(t.Context()), ErrUnavailable)
	require.NoError(t, NewModule(nil, Config{}).Stop(t.Context()))
	for _, config := range []Config{{Queues: map[string]int{"bad/name": 1}}, {Queues: map[string]int{"default": 0}}, {PollInterval: -time.Second}, {DrainTimeout: -time.Second}, {ClaimBatch: -1}} {
		invalid := catalogModule()
		defaults := NewModule(nil, config)
		invalid.config = defaults.config
		require.ErrorIs(t, invalid.Initialize(t.Context()), ErrInvalidRequest)
	}
	owner, _ := workerHarness(t, 1)
	owner.onDead = nil
	require.ErrorIs(t, owner.Start(t.Context()), ErrUnavailable)
	enabled := false
	owner.config.WorkersEnabled = &enabled
	owner.config.SchedulerEnabled = &enabled
	require.NoError(t, owner.Start(t.Context()))
	require.Nil(t, owner.workers)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, owner.Start(canceled), context.Canceled)
}

func TestWorkersDeadLetterFailureRollsBackSettlement(t *testing.T) {
	m, db := workerHarness(t, 1)
	sentinel := errors.New("outbox unavailable")
	m.onDead = func(context.Context, *gorm.DB, DeadLetter) error { return sentinel }
	h := m.handlers["jobs.work"]
	entered := make(chan struct{})
	h.Run = func(context.Context, Job) error { close(entered); return Permanent(errors.New("permanent")) }
	m.handlers[h.Kind] = h
	row, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: h.Kind})
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}
	require.NoError(t, m.Quiesce(t.Context()))
	require.Equal(t, "RUNNING", storedJob(t, db, row.JobID).State)
}

func TestWorkerStartupRecoversExpiredAttempt(t *testing.T) {
	m, db := workerHarness(t, 1)
	claim := runningJob(t, m)
	h := m.handlers[claim.Kind]
	h.BackoffBase = time.Millisecond
	h.BackoffMax = time.Millisecond
	m.handlers[claim.Kind] = h
	require.NoError(t, db.Table("j_background_jobs").Where("id = ?", claim.ID).Update("lease_expires_at_ms", -1).Error)
	require.NoError(t, m.Start(t.Context()))
	require.Eventually(t, func() bool { return storedJob(t, db, claim.ID).State == "SUCCEEDED" }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Quiesce(t.Context()))
	row := storedJob(t, db, claim.ID)
	require.Equal(t, 2, row.Attempts)
	require.Equal(t, "LEASE_EXPIRED", row.LastErrorCode)
}
