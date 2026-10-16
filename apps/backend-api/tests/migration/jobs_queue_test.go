package migration_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestJobsQueuePostgresTransactionsAndConcurrency(t *testing.T) {
	raw, dsn := testdb.OpenPostgresWithDSN(t)
	for _, source := range []goosex.Source{(&dlock.Module{}).MigrationSource(), (&jobs.Module{}).MigrationSource()} {
		source.Prefix = deploymentPrefix
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "postgres"))
	}
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "postgres", DSN: dsn, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(deploymentPrefix))
	ids, err := guid.New(1)
	require.NoError(t, err)
	makeQueue := func() jobs.Queue {
		locks := dlock.NewModule(rt)
		owner := jobs.NewModule(rt, jobs.Config{MaxPendingPerTenant: 3})
		services := module.NewServices()
		require.NoError(t, locks.ProvideServices(services))
		require.NoError(t, owner.ProvideServices(services))
		require.NoError(t, module.Provide[guid.Source](services, ids))
		require.NoError(t, module.Provide[audit.Recorder](services, jobsQueueRecorder{}))
		require.NoError(t, services.Seal())
		require.NoError(t, owner.InjectServices(services))
		require.NoError(t, owner.AddHandlers("jobs", jobs.Handler{Kind: "jobs.work", Run: func(context.Context, jobs.Job) error { return nil }}))
		require.NoError(t, owner.Initialize(t.Context()))
		queue, err := module.Resolve[jobs.Queue](services)
		require.NoError(t, err)
		return queue
	}
	// Independent queue instances share only the database and the GUID service.
	first, second := makeQueue(), makeQueue()
	db := router.Write()
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(12)
	abort := errors.New("business rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := first.Enqueue(t.Context(), tx, jobs.EnqueueRequest{TenantID: 1, Kind: "jobs.work", IdempotencyKey: "rollback"})
		require.NoError(t, err)
		return abort
	}), abort)
	var count int64
	require.NoError(t, db.Table(deploymentPrefix+"background_jobs").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := first.Enqueue(t.Context(), tx, jobs.EnqueueRequest{TenantID: 2, Kind: "jobs.work"})
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		waitCount := pool.Stats().WaitCount
		_, err = second.Enqueue(ctx, nil, jobs.EnqueueRequest{TenantID: 2, Kind: "jobs.work"})
		require.Error(t, err)
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
		require.Equal(t, waitCount, pool.Stats().WaitCount, "must block on DB row, not pool exhaustion")
		return nil
	}))
	_, err = first.Enqueue(t.Context(), nil, jobs.EnqueueRequest{TenantID: 2, Kind: "jobs.work"})
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := range 10 {
		queue := first
		if i%2 == 1 {
			queue = second
		}
		wg.Go(func() {
			_, err := queue.Enqueue(t.Context(), nil, jobs.EnqueueRequest{TenantID: 3, Kind: "jobs.work"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, jobs.ErrQueueFull)
		}
	}
	require.Equal(t, 3, successes)
	require.NoError(t, db.Table(deploymentPrefix+"background_jobs").Where("tenant_id=?", 3).Count(&count).Error)
	require.EqualValues(t, 3, count)
	dedup := make(chan jobs.EnqueueResult, 10)
	errs := make(chan error, 10)
	for i := range 10 {
		queue := first
		if i%2 == 1 {
			queue = second
		}
		wg.Go(func() {
			r, err := queue.Enqueue(t.Context(), nil, jobs.EnqueueRequest{TenantID: 4, Kind: "jobs.work", IdempotencyKey: "same"})
			dedup <- r
			errs <- err
		})
	}
	wg.Wait()
	close(dedup)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var id guid.ID
	created := 0
	for r := range dedup {
		if r.Created {
			created++
		}
		if id == 0 {
			id = r.JobID
		}
		require.Equal(t, id, r.JobID)
	}
	require.Equal(t, 1, created)
}

// Queue-only tests do not execute handlers.
type jobsQueueRecorder struct{}

func (jobsQueueRecorder) Record(context.Context, audit.Event) {}

func TestJobsWorkersPostgresLifecycle(t *testing.T) {
	raw, dsn := testdb.OpenPostgresWithDSN(t)
	for _, source := range []goosex.Source{(&dlock.Module{}).MigrationSource(), (&jobs.Module{}).MigrationSource()} {
		source.Prefix = deploymentPrefix
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "postgres"))
	}
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "postgres", DSN: dsn, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(deploymentPrefix))
	ids, err := guid.New(1)
	require.NoError(t, err)
	var active, peak atomic.Int32
	var mu sync.Mutex
	calls := map[guid.ID]int{}
	handler := jobs.Handler{Kind: "jobs.work", Run: func(ctx context.Context, job jobs.Job) error {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		mu.Lock()
		calls[job.ID]++
		mu.Unlock()
		timer := time.NewTimer(20 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
	owners := make([]*jobs.Module, 2)
	for i := range owners {
		owner := jobs.NewModule(rt, jobs.Config{MaxRunningPerTenant: 3, Queues: map[string]int{"default": 4}, PollInterval: 10 * time.Millisecond, DrainTimeout: time.Second})
		locks := dlock.NewModule(rt)
		services := module.NewServices()
		require.NoError(t, locks.ProvideServices(services))
		require.NoError(t, owner.ProvideServices(services))
		require.NoError(t, module.Provide[guid.Source](services, ids))
		require.NoError(t, module.Provide[audit.Recorder](services, jobsQueueRecorder{}))
		require.NoError(t, services.Seal())
		connector, err := module.Resolve[jobs.DeadLetterConnector](services)
		require.NoError(t, err)
		require.NoError(t, connector.Bind(func(context.Context, *gorm.DB, jobs.DeadLetter) error { return errors.New("unexpected dead letter") }))
		require.NoError(t, owner.InjectServices(services))
		require.NoError(t, owner.AddHandlers("jobs", handler))
		require.NoError(t, owner.Initialize(t.Context()))
		owners[i] = owner
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, owner.Stop(ctx))
		})
	}
	requests := make([]jobs.EnqueueRequest, 24)
	for i := range requests {
		requests[i] = jobs.EnqueueRequest{TenantID: 1, Kind: handler.Kind}
	}
	rows, err := owners[0].EnqueueBatch(t.Context(), nil, requests)
	require.NoError(t, err)
	for _, owner := range owners {
		require.NoError(t, owner.Start(t.Context()))
	}
	db := router.Write()
	require.Eventually(t, func() bool {
		var count int64
		require.NoError(t, db.Table(deploymentPrefix+"background_jobs").Where("state = ?", "SUCCEEDED").Count(&count).Error)
		return count == int64(len(rows))
	}, 10*time.Second, 20*time.Millisecond)
	for _, owner := range owners {
		require.NoError(t, owner.Quiesce(t.Context()))
	}
	require.LessOrEqual(t, peak.Load(), int32(3))
	require.Positive(t, peak.Load())
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, len(rows))
	for _, row := range rows {
		require.Equal(t, 1, calls[row.JobID])
	}
}

func TestJobsSchedulerPostgresThreeReplicasAndHandoff(t *testing.T) {
	raw, dsn := testdb.OpenPostgresWithDSN(t)
	for _, source := range []goosex.Source{(&dlock.Module{}).MigrationSource(), (&jobs.Module{}).MigrationSource()} {
		source.Prefix = deploymentPrefix
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "postgres"))
	}
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "postgres", DSN: dsn, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(deploymentPrefix))
	ids, err := guid.New(1)
	require.NoError(t, err)
	var mu sync.Mutex
	calls := map[guid.ID]int{}
	owners := make([]*jobs.Module, 3)
	for i := range owners {
		owner := jobs.NewModule(rt, jobs.Config{Queues: map[string]int{"default": 1}, PollInterval: 20 * time.Millisecond, DrainTimeout: time.Second})
		locks := dlock.NewModule(rt)
		services := module.NewServices()
		require.NoError(t, locks.ProvideServices(services))
		require.NoError(t, owner.ProvideServices(services))
		require.NoError(t, module.Provide[guid.Source](services, ids))
		require.NoError(t, module.Provide[audit.Recorder](services, jobsQueueRecorder{}))
		require.NoError(t, services.Seal())
		require.NoError(t, owner.InjectServices(services))
		deadletter, err := module.Resolve[jobs.DeadLetterConnector](services)
		require.NoError(t, err)
		require.NoError(t, deadletter.Bind(func(context.Context, *gorm.DB, jobs.DeadLetter) error { return errors.New("unexpected deadletter") }))
		require.NoError(t, owner.AddHandlers("jobs", jobs.Handler{Kind: "jobs.tick", Run: func(_ context.Context, job jobs.Job) error { mu.Lock(); calls[job.ID]++; mu.Unlock(); return nil }}))
		require.NoError(t, owner.AddSchedules("jobs", jobs.Schedule{Name: "jobs.every_second", Kind: "jobs.tick", Interval: time.Second}))
		require.NoError(t, owner.Initialize(t.Context()))
		owners[i] = owner
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, owner.Stop(ctx))
		})
	}
	db := router.Write()
	count := func() int64 {
		var n int64
		require.NoError(t, db.Table(deploymentPrefix+"background_jobs").Where("state = ?", "SUCCEEDED").Count(&n).Error)
		return n
	}
	require.NoError(t, owners[0].Start(t.Context()))
	require.Eventually(t, func() bool {
		var n int64
		require.NoError(t, db.Table(deploymentPrefix+"background_job_schedules").Count(&n).Error)
		return n == 1
	}, 3*time.Second, 10*time.Millisecond)
	// The first instance already owns leadership; other replicas now compete
	// while all three independently consume the same durable job queue.
	require.NoError(t, owners[1].Start(t.Context()))
	require.NoError(t, owners[2].Start(t.Context()))
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test canceled")
	}
	completed := count()
	require.GreaterOrEqual(t, completed, int64(9))
	require.LessOrEqual(t, completed, int64(11))
	require.NoError(t, owners[0].Quiesce(t.Context()))
	require.Eventually(t, func() bool { return count() > completed }, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, owners[1].Quiesce(t.Context()))
	require.NoError(t, owners[2].Quiesce(t.Context()))
	var rows []struct {
		ID             guid.ID
		IdempotencyKey string
	}
	require.NoError(t, db.Table(deploymentPrefix+"background_jobs").Select("id,idempotency_key").Find(&rows).Error)
	keys := map[string]bool{}
	mu.Lock()
	defer mu.Unlock()
	for _, row := range rows {
		require.False(t, keys[row.IdempotencyKey], "duplicate firing key")
		keys[row.IdempotencyKey] = true
		require.Equal(t, 1, calls[row.ID])
	}
}
