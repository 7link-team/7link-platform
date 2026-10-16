package jobs

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func scheduleOwner(t *testing.T, base *Module, schedules ...Schedule) *Module {
	t.Helper()
	m := NewModule(base.runtime, base.config)
	m.ids, m.guard, m.acquire, m.tryAcquire, m.recorder = base.ids, base.guard, base.acquire, base.tryAcquire, base.recorder
	if base.activeTenants != nil {
		require.NoError(t, m.bindTenantFanout(base.activeTenants))
	}
	for _, h := range base.handlers {
		if h.Kind == fanoutKind && base.activeTenants != nil {
			continue
		}
		require.NoError(t, m.AddHandlers("jobs", h))
	}
	require.NoError(t, m.AddSchedules("jobs", schedules...))
	require.NoError(t, m.Initialize(t.Context()))
	return m
}
func storedSchedule(t *testing.T, db *gorm.DB, name string) model.Schedule {
	t.Helper()
	var row model.Schedule
	require.NoError(t, db.Table("j_background_job_schedules").Where("name = ?", name).Take(&row).Error)
	return row
}

func TestScheduleReconciliationPreservesProgressPausesAndHistory(t *testing.T) {
	base, db := queueHarness(t, 10)
	declaration := Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour, Payload: map[string]any{"count": 2}}
	m := scheduleOwner(t, base, declaration)
	require.NoError(t, m.reconcileSchedules(t.Context()))
	first := storedSchedule(t, db, declaration.Name)
	require.Equal(t, first.CreatedAt.UnixMilli()+time.Hour.Milliseconds(), first.NextRunAtMS)
	require.True(t, first.Enabled)
	require.Nil(t, first.Cron)
	require.NotNil(t, first.IntervalMS)
	require.Len(t, first.DefinitionHash, 64)
	require.NoError(t, db.Table("j_background_job_schedules").Where("id = ?", first.ID).Updates(map[string]any{"enabled": false, "last_fired_at_ms": 123, "last_job_id": 42, "next_run_at_ms": 456}).Error)
	paused := storedSchedule(t, db, declaration.Name)
	restarted := scheduleOwner(t, base, declaration)
	require.NoError(t, restarted.reconcileSchedules(t.Context()))
	require.Equal(t, paused, storedSchedule(t, db, declaration.Name))
	declaration.Interval = 0
	declaration.Cron = "0 9 * * *"
	declaration.Timezone = "Asia/Singapore"
	changed := scheduleOwner(t, base, declaration)
	require.NoError(t, changed.reconcileSchedules(t.Context()))
	updated := storedSchedule(t, db, declaration.Name)
	require.Equal(t, first.ID, updated.ID)
	require.False(t, updated.Enabled)
	require.Nil(t, updated.IntervalMS)
	require.Equal(t, declaration.Cron, *updated.Cron)
	require.Equal(t, paused.LastFiredAtMS, updated.LastFiredAtMS)
	require.Equal(t, paused.LastJobID, updated.LastJobID)
	require.Equal(t, paused.Version+1, updated.Version)
	require.NotEqual(t, paused.DefinitionHash, updated.DefinitionHash)
	require.Greater(t, updated.NextRunAtMS, updated.UpdatedAt.UnixMilli())
	require.Equal(t, first.CreatedAt, updated.CreatedAt)
}

func TestScheduleRemovalDisablesWithoutDeletingOrResurrecting(t *testing.T) {
	base, db := queueHarness(t, 10)
	declaration := Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour}
	m := scheduleOwner(t, base, declaration)
	require.NoError(t, m.reconcileSchedules(t.Context()))
	before := storedSchedule(t, db, declaration.Name)
	empty := scheduleOwner(t, base)
	require.NoError(t, empty.reconcileSchedules(t.Context()))
	orphan := storedSchedule(t, db, declaration.Name)
	require.False(t, orphan.Enabled)
	require.Equal(t, before.Version+1, orphan.Version)
	require.Equal(t, before.NextRunAtMS, orphan.NextRunAtMS)
	require.NoError(t, empty.reconcileSchedules(t.Context()))
	require.Equal(t, orphan, storedSchedule(t, db, declaration.Name))
	require.NoError(t, m.reconcileSchedules(t.Context()))
	require.Equal(t, orphan, storedSchedule(t, db, declaration.Name))
}

func TestScheduleConcurrentReconciliationAndAtomicFailure(t *testing.T) {
	base, db := queueHarness(t, 10)
	declaration := Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour}
	owners := []*Module{scheduleOwner(t, base, declaration), scheduleOwner(t, base, declaration)}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, owner := range owners {
		wg.Add(1)
		go func() { defer wg.Done(); results <- owner.reconcileSchedules(t.Context()) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, db.Table("j_background_job_schedules").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, 1, storedSchedule(t, db, declaration.Name).Version)
	// A syntactically valid but impossible calendar cannot partially apply the
	// preceding definition update or orphan removal.
	declaration.Interval = 2 * time.Hour
	broken := scheduleOwner(t, base, declaration, Schedule{Name: "jobs.z_impossible", Kind: "jobs.work", Cron: "0 0 31 2 *"})
	before := storedSchedule(t, db, declaration.Name)
	require.ErrorIs(t, broken.reconcileSchedules(t.Context()), ErrInvalidDeclaration)
	require.Equal(t, before, storedSchedule(t, db, declaration.Name))
	require.NoError(t, db.Table("j_background_job_schedules").Count(&count).Error)
	require.EqualValues(t, 1, count)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, owners[0].reconcileSchedules(canceled), context.Canceled)
}

func TestScheduleCalendarAndDefinitionFingerprint(t *testing.T) {
	interval, _ := normalizeSchedule("jobs", Schedule{Name: "jobs.interval", Kind: "jobs.work", Interval: time.Second})
	_, err := nextScheduleTime(interval, math.MaxInt64)
	require.ErrorIs(t, err, ErrInvalidDeclaration)
	for _, test := range []struct{ cron, zone, after, want string }{
		{"0 9 * * *", "Asia/Singapore", "2026-10-16T01:00:00Z", "2026-10-17T01:00:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-08T06:59:00Z", "2026-03-09T06:30:00Z"},
		{"0 0 29 2 *", "UTC", "2026-01-01T00:00:00Z", "2028-02-29T00:00:00Z"},
	} {
		s, err := normalizeSchedule("jobs", Schedule{Name: "jobs.calendar", Kind: "jobs.work", Cron: test.cron, Timezone: test.zone})
		require.NoError(t, err)
		after, err := time.Parse(time.RFC3339, test.after)
		require.NoError(t, err)
		next, err := nextScheduleTime(s, after.UnixMilli())
		require.NoError(t, err)
		require.Equal(t, test.want, time.UnixMilli(next).UTC().Format(time.RFC3339))
	}
	first, err := scheduleDefinition(interval, "default", 1000)
	require.NoError(t, err)
	later, err := scheduleDefinition(interval, "default", 2000)
	require.NoError(t, err)
	require.Equal(t, first.DefinitionHash, later.DefinitionHash)
	moved, err := scheduleDefinition(interval, "notifications", 1000)
	require.NoError(t, err)
	require.NotEqual(t, first.DefinitionHash, moved.DefinitionHash)
	bad := interval
	bad.Payload = func() {}
	_, err = scheduleDefinition(bad, "default", 0)
	require.ErrorIs(t, err, ErrInvalidPayload)
	_, err = nextScheduleTime(Schedule{Cron: "bad", Timezone: "UTC"}, 0)
	require.Error(t, err)
	_, err = nextScheduleTime(Schedule{Cron: "* * * * *", Timezone: "Missing/Zone"}, 0)
	require.Error(t, err)
}

func TestScheduleReconcileRequiresReadyDependencies(t *testing.T) {
	base, _ := queueHarness(t, 10)
	require.ErrorIs(t, NewModule(nil, Config{}).reconcileSchedules(t.Context()), ErrUnavailable)
	m := scheduleOwner(t, base, Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour})
	delete(m.handlers, "jobs.work")
	require.ErrorIs(t, m.reconcileSchedules(t.Context()), ErrUnknownKind)
	m = scheduleOwner(t, base)
	failure := errors.New("guard unavailable")
	m.guard = func(context.Context, *gorm.DB, string) error { return failure }
	require.ErrorIs(t, m.reconcileSchedules(t.Context()), failure)
}

func dueSchedule(t *testing.T, db *gorm.DB, name string, behind time.Duration) model.Schedule {
	t.Helper()
	row := storedSchedule(t, db, name)
	require.NoError(t, db.Table("j_background_job_schedules").Where("id = ?", row.ID).Update("next_run_at_ms", row.CreatedAt.UnixMilli()-behind.Milliseconds()).Error)
	return storedSchedule(t, db, name)
}

func TestScheduleFireIsAtomicAndDeduplicatedAcrossLeaders(t *testing.T) {
	base, db := queueHarness(t, 10)
	s := Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Second, Payload: map[string]any{"x": 7}}
	owners := []*Module{scheduleOwner(t, base, s), scheduleOwner(t, base, s)}
	require.NoError(t, owners[0].reconcileSchedules(t.Context()))
	due := dueSchedule(t, db, s.Name, 10*time.Second)
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, m := range owners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fired, err := m.fireSchedule(t.Context(), due.ID)
			results <- fired
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for fired := range results {
		if fired {
			count++
		}
	}
	require.Equal(t, 1, count)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, queueCount(t, db))
	after := storedSchedule(t, db, s.Name)
	require.Equal(t, due.NextRunAtMS, *after.LastFiredAtMS)
	require.Equal(t, after.UpdatedAt.UnixMilli()+1000, after.NextRunAtMS)
	require.Equal(t, due.Version+1, after.Version)
	job := storedJob(t, db, *after.LastJobID)
	require.Zero(t, job.TenantID)
	require.Equal(t, "PENDING", job.State)
	require.JSONEq(t, `{"x":7}`, job.Payload)
	require.Equal(t, s.Name, *job.ConcurrencyKey)
	require.Equal(t, scheduleFireKey(s.Name, due.NextRunAtMS), *job.IdempotencyKey)
	// Simulate a stale progress snapshot after the corresponding enqueue already
	// committed. The unique key must reuse that job, including at full quota.
	require.NoError(t, db.Table("j_background_job_schedules").Where("id = ?", due.ID).Update("next_run_at_ms", due.NextRunAtMS).Error)
	owners[0].config.MaxPendingPerTenant = 1
	fired, err := owners[0].fireSchedule(t.Context(), due.ID)
	require.NoError(t, err)
	require.True(t, fired)
	require.EqualValues(t, 1, queueCount(t, db))
	require.Equal(t, after.LastJobID, storedSchedule(t, db, s.Name).LastJobID)
}

func TestScheduleEnqueueAndProgressRollbackTogether(t *testing.T) {
	base, db := queueHarness(t, 10)
	s := Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Minute}
	m := scheduleOwner(t, base, s)
	require.NoError(t, m.reconcileSchedules(t.Context()))
	before := dueSchedule(t, db, s.Name, time.Second)
	require.NoError(t, db.Exec("CREATE TRIGGER reject_progress BEFORE UPDATE ON j_background_job_schedules BEGIN SELECT RAISE(ABORT, 'progress rejected'); END").Error)
	fired, err := m.fireSchedule(t.Context(), before.ID)
	require.ErrorContains(t, err, "progress rejected")
	require.False(t, fired)
	require.Zero(t, queueCount(t, db))
	require.Equal(t, before, storedSchedule(t, db, s.Name))
	require.NoError(t, db.Exec("DROP TRIGGER reject_progress").Error)
	m.config.MaxPendingPerTenant = 1
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work", IdempotencyKey: "full"})
	require.NoError(t, err)
	fired, err = m.fireSchedule(t.Context(), before.ID)
	require.ErrorIs(t, err, ErrQueueFull)
	require.False(t, fired)
	require.Equal(t, before, storedSchedule(t, db, s.Name))
	_, err = m.Cancel(t.Context(), nil, 0, "jobs.work", "full")
	require.NoError(t, err)
	fired, err = m.fireSchedule(t.Context(), before.ID)
	require.NoError(t, err)
	require.True(t, fired)
}

func TestScheduleSkipDoesNotSuppressOrdinaryPollingDelay(t *testing.T) {
	base, db := queueHarness(t, 10)
	s := Schedule{Name: "jobs.skip", Kind: "jobs.work", Interval: time.Minute, Misfire: MisfireSkip}
	m := scheduleOwner(t, base, s)
	require.NoError(t, m.reconcileSchedules(t.Context()))
	before := dueSchedule(t, db, s.Name, 2*time.Minute)
	fired, err := m.fireSchedule(t.Context(), before.ID)
	require.NoError(t, err)
	require.False(t, fired)
	skipped := storedSchedule(t, db, s.Name)
	require.Greater(t, skipped.NextRunAtMS, skipped.UpdatedAt.UnixMilli())
	require.Nil(t, skipped.LastFiredAtMS)
	require.Nil(t, skipped.LastJobID)
	require.Zero(t, queueCount(t, db))
	dueSchedule(t, db, s.Name, time.Millisecond)
	fired, err = m.fireSchedule(t.Context(), before.ID)
	require.NoError(t, err)
	require.True(t, fired)
	require.EqualValues(t, 1, queueCount(t, db))
}

func TestScheduleStaleLeaderPausedAndUnknownDoNotFire(t *testing.T) {
	base, db := queueHarness(t, 10)
	s := Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Hour}
	old := scheduleOwner(t, base, s)
	require.NoError(t, old.reconcileSchedules(t.Context()))
	s.Interval = 2 * time.Hour
	current := scheduleOwner(t, base, s)
	require.NoError(t, current.reconcileSchedules(t.Context()))
	due := dueSchedule(t, db, s.Name, time.Second)
	fired, err := old.fireSchedule(t.Context(), due.ID)
	require.NoError(t, err)
	require.False(t, fired)
	empty := scheduleOwner(t, base)
	fired, err = empty.fireSchedule(t.Context(), due.ID)
	require.NoError(t, err)
	require.False(t, fired)
	require.NoError(t, db.Table("j_background_job_schedules").Where("id = ?", due.ID).Update("enabled", false).Error)
	fired, err = current.fireSchedule(t.Context(), due.ID)
	require.NoError(t, err)
	require.False(t, fired)
	fired, err = current.fireSchedule(t.Context(), 1)
	require.NoError(t, err)
	require.False(t, fired)
	_, err = current.fireSchedule(t.Context(), 0)
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Zero(t, queueCount(t, db))
}

func TestSchedulePerTenantRequiresEnumerator(t *testing.T) {
	m := catalogModule()
	require.NoError(t, m.AddHandlers("jobs", catalogHandler("jobs.work")))
	require.NoError(t, m.AddSchedules("jobs", Schedule{Name: "jobs.tenants", Kind: "jobs.work", Interval: time.Hour, TenantScope: ScopePerTenant}))
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
	base, _ := queueHarness(t, 10)
	empty := scheduleOwner(t, base)
	fired, err := empty.fireDueSchedules(t.Context())
	require.NoError(t, err)
	require.Zero(t, fired)
	_, err = NewModule(nil, Config{}).fireDueSchedules(t.Context())
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestScheduleFireKeyRetainsLongNamesWithoutCollisions(t *testing.T) {
	require.Equal(t, "schedule:jobs.tick:123", scheduleFireKey("jobs.tick", 123))
	first := scheduleFireKey(strings.Repeat("a", 128), 123)
	require.True(t, validKey(first))
	require.Equal(t, first, scheduleFireKey(strings.Repeat("a", 128), 123))
	require.NotEqual(t, first, scheduleFireKey(strings.Repeat("a", 127)+"b", 123))
	require.NotEqual(t, first, scheduleFireKey(strings.Repeat("a", 128), 124))
}

func TestSchedulerLeadershipHandoffPreservesPlanAndStopsAfterQuiesce(t *testing.T) {
	base, db := queueHarness(t, 100)
	disabled := false
	base.config.WorkersEnabled = &disabled
	base.config.PollInterval = 20 * time.Millisecond
	base.config.DrainTimeout = time.Second
	declaration := Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Second}
	owners := []*Module{scheduleOwner(t, base, declaration), scheduleOwner(t, base, declaration)}
	leaders := make(chan int, 2)
	for i, owner := range owners {
		original := owner.acquire
		owner.acquire = func(ctx context.Context, name string, ttl time.Duration, lost func(string, error)) (*dlock.Lock, error) {
			lock, err := original(ctx, name, ttl, lost)
			if err == nil {
				leaders <- i
			}
			return lock, err
		}
		require.NoError(t, owner.Start(t.Context())) // scheduler-only needs no deadletter execution dependency
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			require.NoError(t, owner.Stop(ctx))
		})
	}
	var leader int
	select {
	case leader = <-leaders:
	case <-time.After(3 * time.Second):
		t.Fatal("no scheduler leader")
	}
	require.Eventually(t, func() bool { return queueCount(t, db) >= 2 }, 5*time.Second, 20*time.Millisecond)
	select {
	case <-leaders:
		t.Fatal("two simultaneous leaders")
	default:
	}
	before := storedSchedule(t, db, declaration.Name)
	require.NoError(t, owners[leader].Quiesce(t.Context()))
	select {
	case follower := <-leaders:
		require.NotEqual(t, leader, follower)
	case <-time.After(3 * time.Second):
		t.Fatal("follower did not acquire released lease")
	}
	require.Eventually(t, func() bool { return queueCount(t, db) >= 3 }, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, owners[1-leader].Quiesce(t.Context()))
	after := storedSchedule(t, db, declaration.Name)
	require.Equal(t, before.ID, after.ID)
	require.Greater(t, *after.LastFiredAtMS, *before.LastFiredAtMS)
	var pending int64
	require.NoError(t, db.Table("j_background_jobs").Where("state = ?", "PENDING").Count(&pending).Error)
	require.Equal(t, queueCount(t, db), pending)
	var duplicate int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM (SELECT idempotency_key FROM j_background_jobs GROUP BY idempotency_key HAVING COUNT(*) > 1)").Scan(&duplicate).Error)
	require.Zero(t, duplicate)
	total := queueCount(t, db)
	require.Never(t, func() bool { return queueCount(t, db) != total }, 1100*time.Millisecond, 50*time.Millisecond)
}

func TestSchedulerLifecycleRunsTenantFanoutAndChildren(t *testing.T) {
	base, db := queueHarness(t, 100)
	base.activeTenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{1, 2}, nil }
	base.config.Queues = map[string]int{"default": 1}
	base.config.PollInterval = 10 * time.Millisecond
	m := scheduleOwner(t, base, Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Second, TenantScope: ScopePerTenant})
	m.onDead = func(context.Context, *gorm.DB, DeadLetter) error { return errors.New("unexpected deadletter") }
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, m.Stop(ctx))
	})
	require.Eventually(t, func() bool {
		var count int64
		require.NoError(t, db.Table("j_background_jobs").Where("kind = ? AND tenant_id > 0 AND state = ?", "jobs.work", "SUCCEEDED").Count(&count).Error)
		return count >= 2
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Quiesce(t.Context()))
	var dispatchers int64
	require.NoError(t, db.Table("j_background_jobs").Where("kind = ? AND state = ?", fanoutKind, "SUCCEEDED").Count(&dispatchers).Error)
	require.Positive(t, dispatchers)
}

func TestSchedulerDisabledStillExecutesManuallyQueuedJobs(t *testing.T) {
	base, db := queueHarness(t, 10)
	disabled := false
	base.config.SchedulerEnabled = &disabled
	base.config.PollInterval = 10 * time.Millisecond
	m := scheduleOwner(t, base, Schedule{Name: "jobs.tick", Kind: "jobs.work", Interval: time.Second})
	m.onDead = func(context.Context, *gorm.DB, DeadLetter) error { return nil }
	require.NoError(t, m.reconcileSchedules(t.Context()))
	before := dueSchedule(t, db, "jobs.tick", time.Second)
	queued, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: "jobs.work"})
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, m.Stop(ctx))
	})
	require.Eventually(t, func() bool { return storedJob(t, db, queued.JobID).State == "SUCCEEDED" }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Quiesce(t.Context()))
	require.Equal(t, before, storedSchedule(t, db, "jobs.tick"))
	require.EqualValues(t, 1, queueCount(t, db))
}

func TestScheduleIntervalOverrideRestartAndChangePreservePause(t *testing.T) {
	base, db := queueHarness(t, 10)
	interval := 2 * time.Hour
	base.config.Schedules = map[string]ScheduleOverride{"jobs.hourly": {Interval: &interval}}
	declaration := Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour}
	m := scheduleOwner(t, base, declaration)
	require.NoError(t, m.reconcileSchedules(t.Context()))
	first := storedSchedule(t, db, declaration.Name)
	require.Equal(t, interval.Milliseconds(), *first.IntervalMS)
	require.NoError(t, db.Table("j_background_job_schedules").Where("id = ?", first.ID).Updates(map[string]any{"enabled": false, "next_run_at_ms": 456, "last_fired_at_ms": 123, "last_job_id": 42}).Error)
	paused := storedSchedule(t, db, declaration.Name)
	restarted := scheduleOwner(t, base, declaration)
	require.NoError(t, restarted.reconcileSchedules(t.Context()))
	require.Equal(t, paused, storedSchedule(t, db, declaration.Name))
	interval = 3 * time.Hour
	changed := scheduleOwner(t, base, declaration)
	require.NoError(t, changed.reconcileSchedules(t.Context()))
	updated := storedSchedule(t, db, declaration.Name)
	require.False(t, updated.Enabled)
	require.Equal(t, interval.Milliseconds(), *updated.IntervalMS)
	require.Equal(t, paused.LastJobID, updated.LastJobID)
	require.Equal(t, paused.LastFiredAtMS, updated.LastFiredAtMS)
	require.Equal(t, paused.Version+1, updated.Version)
	require.Equal(t, updated.UpdatedAt.UnixMilli()+interval.Milliseconds(), updated.NextRunAtMS)
}
