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
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

func queueHarness(t *testing.T, pendingLimit int) (*Module, *gorm.DB) {
	t.Helper()
	m, db := singletonHarness(t, catalogHandler("jobs.work"), catalogHandler("jobs.other"))
	m.config.MaxPendingPerTenant = pendingLimit
	raw, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "j_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	return m, db
}
func queueCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table("j_background_jobs").Count(&count).Error)
	return count
}
func TestQueueTransactionIdempotencyAndCancellation(t *testing.T) {
	m, db := queueHarness(t, 2)
	require.NoError(t, db.Exec("CREATE TABLE business_probe (id INTEGER PRIMARY KEY)").Error)
	req := EnqueueRequest{TenantID: 1, Kind: "jobs.work", Payload: map[string]any{"x": 1}, IdempotencyKey: "once", CreatedBy: 7, Delay: time.Second, ConcurrencyKey: "tenant-1"}
	abort := errors.New("business failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("INSERT INTO business_probe VALUES (1)").Error)
		result, err := m.Enqueue(t.Context(), tx, req)
		require.NoError(t, err)
		require.True(t, result.Created)
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.Zero(t, queueCount(t, db))
	var businessCount int64
	require.NoError(t, db.Table("business_probe").Count(&businessCount).Error)
	require.Zero(t, businessCount)
	result, err := m.Enqueue(t.Context(), nil, req)
	require.NoError(t, err)
	require.True(t, result.Created)
	duplicate, err := m.Enqueue(t.Context(), nil, req)
	require.NoError(t, err)
	require.False(t, duplicate.Created)
	require.Equal(t, result.JobID, duplicate.JobID)
	var row model.Job
	require.NoError(t, db.Table("j_background_jobs").First(&row).Error)
	require.JSONEq(t, `{"x":1}`, row.Payload)
	require.JSONEq(t, `[]`, row.ErrorHistory)
	require.Equal(t, row.CreatedAt.UnixMilli()+1000, row.RunAtMS)
	require.Equal(t, guid.ID(7), *row.CreatedBy)
	require.Equal(t, "tenant-1", *row.ConcurrencyKey)
	require.Equal(t, 8, row.MaxAttempts)
	require.EqualValues(t, 300000, row.TimeoutMS)
	changed, err := m.Cancel(t.Context(), nil, 1, "jobs.work", "once")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = m.Cancel(t.Context(), nil, 1, "jobs.work", "once")
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, db.Table("j_background_jobs").First(&row).Error)
	require.Equal(t, "CANCELLED", row.State) //nolint:misspell // Spec 09 freezes this state spelling.
	require.NotNil(t, row.FinishedAt)
	require.Equal(t, 2, row.Version)
	duplicate, err = m.Enqueue(t.Context(), nil, req)
	require.NoError(t, err)
	require.False(t, duplicate.Created)
	require.EqualValues(t, 1, queueCount(t, db))
}
func TestQueueBatchQuotaAndRollback(t *testing.T) {
	m, db := queueHarness(t, 2)
	req := EnqueueRequest{TenantID: 1, Kind: "jobs.work"}
	_, err := m.EnqueueBatch(t.Context(), nil, []EnqueueRequest{req, req, req})
	require.ErrorIs(t, err, ErrQueueFull)
	require.Zero(t, queueCount(t, db))
	results, err := m.EnqueueBatch(t.Context(), nil, []EnqueueRequest{req, req})
	require.NoError(t, err)
	require.NotEqual(t, results[0].JobID, results[1].JobID)
	require.EqualValues(t, 2, queueCount(t, db))
	_, err = m.Enqueue(t.Context(), nil, req)
	require.ErrorIs(t, err, ErrQueueFull)
	req.TenantID = 2
	req.IdempotencyKey = "dedup"
	results, err = m.EnqueueBatch(t.Context(), nil, []EnqueueRequest{req, req})
	require.NoError(t, err)
	require.True(t, results[0].Created)
	require.False(t, results[1].Created)
	req.Kind = "jobs.other"
	_, err = m.Enqueue(t.Context(), nil, req)
	require.NoError(t, err)
	req.Kind = "jobs.work"
	_, err = m.Enqueue(t.Context(), nil, req)
	require.NoError(t, err, "idempotent replay remains available at quota")
	changed, err := m.Cancel(t.Context(), nil, 1, "jobs.work", "dedup")
	require.NoError(t, err)
	require.False(t, changed, "tenant boundary")
	req.IdempotencyKey = "new"
	_, err = m.Enqueue(t.Context(), nil, req)
	require.ErrorIs(t, err, ErrQueueFull)
	_, err = m.EnqueueBatch(t.Context(), nil, []EnqueueRequest{{TenantID: 3, Kind: "jobs.work"}, {TenantID: 3, Kind: "missing.work"}})
	require.ErrorIs(t, err, ErrUnknownKind)
	require.EqualValues(t, 4, queueCount(t, db))
}
func TestQueueValidationAndUnavailable(t *testing.T) {
	m, db := queueHarness(t, 10)
	for _, req := range []EnqueueRequest{{TenantID: -1, Kind: "jobs.work"}, {CreatedBy: -1, Kind: "jobs.work"}, {Kind: "jobs.work", Delay: -1}, {Kind: "jobs.work", Priority: 10}, {Kind: "jobs.work", RunAt: time.Now(), Delay: time.Second}, {Kind: "jobs.work", IdempotencyKey: strings.Repeat("x", 129)}, {Kind: "jobs.work", ConcurrencyKey: string([]byte{0xff})}} {
		_, err := m.Enqueue(t.Context(), nil, req)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
	_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work", Payload: []int{1}})
	require.ErrorIs(t, err, ErrInvalidPayload)
	_, err = m.EnqueueBatch(t.Context(), nil, make([]EnqueueRequest, 501))
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = m.Enqueue(t.Context(), db, EnqueueRequest{Kind: "jobs.work"})
	require.ErrorIs(t, err, ErrInvalidRequest)
	for _, args := range []struct {
		tenant    guid.ID
		kind, key string
	}{{-1, "jobs.work", "key"}, {1, "jobs.work", ""}, {1, "bad", "key"}, {1, "jobs.work", strings.Repeat("x", 129)}} {
		_, err = m.Cancel(t.Context(), nil, args.tenant, args.kind, args.key)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
	m.sealed = false
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.ErrorIs(t, err, ErrUnavailable)
	m.sealed = true
	m.runtime = nil
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).Enqueue(t.Context(), nil, EnqueueRequest{})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).Cancel(t.Context(), nil, 1, "jobs.work", "key")
	require.ErrorIs(t, err, ErrUnavailable)
}
func TestQueueConcurrentQuota(t *testing.T) {
	m, db := queueHarness(t, 3)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: "jobs.work"})
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
			require.ErrorIs(t, err, ErrQueueFull)
		}
	}
	require.Equal(t, 3, successes)
	require.EqualValues(t, 3, queueCount(t, db))
}
func TestQueueTimingTraceAndPendingOnlyCancellation(t *testing.T) {
	m, db := queueHarness(t, 10)
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}})
	ctx := trace.ContextWithSpanContext(t.Context(), span)
	when := time.Date(2040, 1, 2, 0, 0, 0, 123000000, time.UTC)
	result, err := m.Enqueue(ctx, nil, EnqueueRequest{Kind: "jobs.work", RunAt: when, Priority: 1, IdempotencyKey: "running"})
	require.NoError(t, err)
	var row model.Job
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", result.JobID).Take(&row).Error)
	require.Equal(t, when.UnixMilli(), row.RunAtMS)
	require.Equal(t, 1, row.Priority)
	require.Equal(t, span.TraceID().String(), row.CorrelationID)
	require.Nil(t, row.CreatedBy)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", result.JobID).Update("state", "RUNNING").Error)
	changed, err := m.Cancel(t.Context(), nil, 0, "jobs.work", "running")
	require.NoError(t, err)
	require.False(t, changed)
	result, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work", Delay: time.Nanosecond})
	require.NoError(t, err)
	row = model.Job{}
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", result.JobID).Take(&row).Error)
	require.Equal(t, row.CreatedAt.UnixMilli()+1, row.RunAtMS)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.Enqueue(ctx, nil, EnqueueRequest{Kind: "jobs.work"})
	require.Error(t, err)
	results, err := m.EnqueueBatch(t.Context(), nil, nil)
	require.NoError(t, err)
	require.Empty(t, results)
}

type failingQueueIDs struct{ err error }

func (s failingQueueIDs) Next() (int64, error) { return 0, s.err }

func TestQueueDependencyFailureRollsBackOwnedTransaction(t *testing.T) {
	m, db := queueHarness(t, 10)
	failure := errors.New("identifier service failed")
	m.ids = failingQueueIDs{failure}
	_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.ErrorIs(t, err, failure)
	require.Zero(t, queueCount(t, db))
	m.guard = func(context.Context, *gorm.DB, string) error { return failure }
	_, err = m.Enqueue(t.Context(), nil, EnqueueRequest{Kind: "jobs.work"})
	require.ErrorIs(t, err, failure)
	_, err = m.Cancel(t.Context(), nil, 1, "jobs.work", "key")
	require.ErrorIs(t, err, failure)
	require.Zero(t, queueCount(t, db))
}
