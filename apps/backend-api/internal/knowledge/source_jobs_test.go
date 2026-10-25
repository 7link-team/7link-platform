package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type knowledgeQueue struct {
	requests []jobs.EnqueueRequest
	failure  error
}

func (q *knowledgeQueue) Enqueue(_ context.Context, _ *gorm.DB, in jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	q.requests = append(q.requests, in)
	return jobs.EnqueueResult{JobID: 99, Created: true}, q.failure
}
func (q *knowledgeQueue) EnqueueBatch(ctx context.Context, tx *gorm.DB, in []jobs.EnqueueRequest) ([]jobs.EnqueueResult, error) {
	out := []jobs.EnqueueResult{}
	for _, r := range in {
		v, e := q.Enqueue(ctx, tx, r)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (*knowledgeQueue) Cancel(context.Context, *gorm.DB, guid.ID, string, string) (bool, error) {
	return false, nil
}

func TestSourceDurableIngestionRechecksIAMCASAndDuplicateDelivery(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	input := &sourceSyncHTTPInput{SourcePathInput: SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: source.ID.String()}, IdempotencyKey: "queue-source", Body: SourceSyncInput{Version: 1}}
	queued, e := m.ingestSourceHTTP(knowledgeContext(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, "PENDING", queued.Body.Data.State)
	require.Equal(t, 2, queued.Body.Data.Version)
	queue := m.queue.(*knowledgeQueue)
	require.Len(t, queue.requests, 1)
	replayed, e := m.ingestSourceHTTP(knowledgeContext(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, "true", replayed.Replayed)
	require.Len(t, queue.requests, 1)
	payload, e := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, e)
	job := jobs.Job{ID: 99, TenantID: 1, Payload: payload}
	require.NoError(t, m.indexSourceJob(t.Context(), job))
	require.NoError(t, m.indexSourceJob(t.Context(), job))
	row, e := m.sourceHTTP(knowledgeContext(t, 2), &input.SourcePathInput)
	require.NoError(t, e)
	require.Equal(t, "INDEXED", row.Body.Data.State)
	require.Equal(t, 3, row.Body.Data.Version)
	chunks := readKnowledgeChunks(t, m, db, row.Body.Data)
	require.Len(t, chunks.Items, 1)
	// A revoked queued actor cannot index even with a valid persisted envelope.
	bad, e := json.Marshal(sourceIndexJob{SourceID: source.ID, Version: 3, ActorID: 8})
	require.NoError(t, e)
	require.Error(t, m.indexSourceJob(t.Context(), jobs.Job{TenantID: 1, Payload: bad}))
	require.Error(t, m.indexSourceJob(t.Context(), jobs.Job{}))
}

func TestSourceReconcileAndPurgeRespectTenantAgeAndActiveChunks(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	m.now = func() time.Time { return now.Add(-8 * 24 * time.Hour) }
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	require.NoError(t, m.reconcileSourcesJob(t.Context(), jobs.Job{ID: 55, TenantID: 1}))
	queue := m.queue.(*knowledgeQueue)
	require.Empty(t, queue.requests)
	m.now = func() time.Time { return now }
	require.NoError(t, m.reconcileSourcesJob(t.Context(), jobs.Job{ID: 56, TenantID: 1}))
	require.Len(t, queue.requests, 1)
	indexed := syncKnowledgeSource(t, m, db, source, nil, false)
	require.NoError(t, m.purgeChunksJob(t.Context(), jobs.Job{TenantID: 1}))
	require.Len(t, readKnowledgeChunks(t, m, db, indexed).Items, 1)
	m.now = func() time.Time { return now.Add(-8 * 24 * time.Hour) }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.invalidateSourceOn(knowledgeContext(t, 2), tx, 1, indexed.ID, indexed.Version)
		return e
	}))
	m.now = func() time.Time { return now }
	require.NoError(t, m.purgeChunksJob(t.Context(), jobs.Job{TenantID: 2}))
	q := query.BindChunks(db, m.names)
	count, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
	require.NoError(t, m.purgeChunksJob(t.Context(), jobs.Job{TenantID: 1}))
	count, e = q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	require.NoError(t, m.purgeChunksJob(t.Context(), jobs.Job{TenantID: 1}))
	require.Len(t, m.JobHandlers(), 4)
	handlers := []string{}
	for _, handler := range m.JobHandlers() {
		handlers = append(handlers, handler.Kind)
	}
	require.ElementsMatch(t, []string{"knowledge.index_source", "knowledge.index_reconcile", "knowledge.chunk_purge", "knowledge.memory_forgetting"}, handlers)
	require.Len(t, m.JobSchedules(), 3)
	schedules := []string{}
	for _, schedule := range m.JobSchedules() {
		schedules = append(schedules, schedule.Name)
		if schedule.Name == "knowledge.memory_forgetting" {
			require.Equal(t, "7 * * * *", schedule.Cron)
			require.Equal(t, "UTC", schedule.Timezone)
		}
	}
	require.ElementsMatch(t, []string{"knowledge.index_reconcile", "knowledge.chunk_purge", "knowledge.memory_forgetting"}, schedules)
}

func TestSourceQueuedActorRevocationPersistsFailedAndStaleJobCannotRevive(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	var queued SourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		queued, e = m.queueSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version})
		return e
	}))
	payload, e := json.Marshal(sourceIndexJob{SourceID: source.ID, Version: queued.Version, ActorID: 2})
	require.NoError(t, e)
	principalLock := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.Error(t, m.indexSourceJob(t.Context(), jobs.Job{ID: 99, TenantID: 1, Payload: payload}))
	m.principals.LockActiveRow = principalLock
	path := &SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: source.ID.String()}
	failed, e := m.sourceHTTP(knowledgeContext(t, 2), path)
	require.NoError(t, e)
	require.Equal(t, "FAILED", failed.Body.Data.State)
	require.Equal(t, "INDEX_AUTHORITY_REVOKED", failed.Body.Data.StateReason)
	require.NoError(t, m.indexSourceJob(t.Context(), jobs.Job{ID: 99, TenantID: 1, Payload: payload}))
	still, e := m.sourceHTTP(knowledgeContext(t, 2), path)
	require.NoError(t, e)
	require.Equal(t, "FAILED", still.Body.Data.State)
}

func TestSourceFinalAttemptCancellationSettlesFailedWithBoundedIndependentContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			m, db, _ := knowledgeHarness(t)
			source := bindKnowledgeSource(t, m, db, sourceDraft())
			var queued SourceView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				queued, e = m.queueSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version})
				return e
			}))
			payload, e := json.Marshal(sourceIndexJob{SourceID: source.ID, Version: queued.Version, ActorID: 2})
			require.NoError(t, e)
			ctx, cancel := context.WithCancel(t.Context())
			var expected error = context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				expected = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			job := jobs.Job{ID: 99, TenantID: 1, Payload: payload, Attempt: 4, MaxAttempts: 5}
			require.ErrorIs(t, m.indexSourceJob(ctx, job), expected)
			path := &SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: source.ID.String()}
			pending, e := m.sourceHTTP(knowledgeContext(t, 2), path)
			require.NoError(t, e)
			require.Equal(t, "PENDING", pending.Body.Data.State)
			job.Attempt = 5
			require.ErrorIs(t, m.indexSourceJob(ctx, job), expected)
			failed, e := m.sourceHTTP(knowledgeContext(t, 2), path)
			require.NoError(t, e)
			require.Equal(t, "FAILED", failed.Body.Data.State)
			require.Equal(t, "INGESTION_RETRIES_EXHAUSTED", failed.Body.Data.StateReason)
			require.Equal(t, queued.Version+1, failed.Body.Data.Version)
			var auditCount int64
			require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "knowledge.source_failed").Count(&auditCount).Error)
			require.EqualValues(t, 1, auditCount)
		})
	}
}

func TestSourceQueueAndFailedSettlementStorageFailuresKeepPriorState(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	failure := errors.New("source queue storage unavailable")
	rollback := errors.New("rollback queue diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("source-queue-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("source-queue-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("source-queue-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("source-queue-failure"))
		require.NoError(t, db.Callback().Create().Remove("source-queue-failure"))
		require.NoError(t, db.Callback().Update().Remove("source-queue-failure"))
	}
	install()
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.queueSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version})
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "queue stage %d", i)
	}
	remove()
	var queued SourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		queued, e = m.queueSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version})
		return e
	}))
	calls, failAt = 0, 0
	install()
	payload := sourceIndexJob{SourceID: source.ID, Version: queued.Version, ActorID: 2}
	job := jobs.Job{TenantID: 1}
	// Audit failure probes the complete write path without committing FAILED.
	appender := m.audit
	m.audit = nil
	require.ErrorIs(t, m.failSourceJob(t.Context(), job, payload, "INGESTION_RETRIES_EXHAUSTED"), ErrUnavailable)
	m.audit = appender
	total = calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, m.failSourceJob(t.Context(), job, payload, "INGESTION_RETRIES_EXHAUSTED"), failure, "settlement stage %d", i)
	}
	remove()
	q := query.BindSources(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, queued.Version, row.Version)
	m.queue.(*knowledgeQueue).failure = failure
	require.NoError(t, m.reconcileSourcesJob(t.Context(), jobs.Job{TenantID: 1}))
	m.runtime = nil
	require.Error(t, m.indexSourceJob(t.Context(), jobs.Job{TenantID: 1, Payload: []byte(`{"source_id":"1","version":1,"actor_id":"2"}`)}))
	require.Error(t, m.reconcileSourcesJob(t.Context(), job))
	require.Error(t, m.purgeChunksJob(t.Context(), job))
	require.Error(t, m.failSourceJob(t.Context(), job, payload, "failed"))
}

func TestSourceReconciliationRetriesOldVersionsAndPropagatesCurrentStorageFailure(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	now := m.now()
	m.now = func() time.Time { return now.Add(-time.Hour) }
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	m.now = func() time.Time { return now }
	failure := errors.New("source recovery unavailable")
	queue := m.queue.(*knowledgeQueue)
	queue.failure = failure
	require.ErrorIs(t, m.reconcileSourcesJob(ctx, jobs.Job{TenantID: 1, ID: 40}), failure)
	queue.failure = nil
	require.NoError(t, m.reconcileSourcesJob(ctx, jobs.Job{TenantID: 1, ID: 41}))
	require.NotEmpty(t, queue.requests)
	indexed := syncKnowledgeSource(t, m, db, source, nil, false)
	q := query.BindSources(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(indexed.ID)).Update(q.ChunkerVersion, sourceChunkerVersion-1)
	require.NoError(t, err)
	require.NoError(t, m.reconcileSourcesJob(ctx, jobs.Job{TenantID: 1, ID: 42}))
	row, err := q.WithContext(ctx).Where(q.ID.Eq(indexed.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, indexed.Version+1, row.Version)
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("recovery-query-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	require.NoError(t, m.reconcileSourcesJob(ctx, jobs.Job{TenantID: 1, ID: 43}))
	total := calls
	require.Positive(t, total)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, m.reconcileSourcesJob(ctx, jobs.Job{TenantID: 1, ID: 44}), failure, "query stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("recovery-query-failure"))
	require.Error(t, m.reconcileSourcesJob(ctx, jobs.Job{}))
}

func TestChunkPurgeStorageAndOutboxFailuresRollBackRetainedContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.invalidateSourceOn(ctx, tx, 1, source.ID, source.Version)
		return err
	}))
	now := m.now()
	m.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	job := jobs.Job{TenantID: 1}
	failure := errors.New("purge persistence unavailable")
	for _, operation := range []string{"query", "delete"} {
		callback := func(tx *gorm.DB) { tx.AddError(failure) }
		if operation == "query" {
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("purge-failure", callback))
		} else {
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("purge-failure", callback))
		}
		require.ErrorIs(t, m.purgeChunksJob(ctx, job), failure)
		if operation == "query" {
			require.NoError(t, db.Callback().Query().Remove("purge-failure"))
		} else {
			require.NoError(t, db.Callback().Delete().Remove("purge-failure"))
		}
	}
	appender, publisher := m.audit, m.events
	m.audit = nil
	require.ErrorIs(t, m.purgeChunksJob(ctx, job), ErrUnavailable)
	m.audit = appender
	m.events = nil
	require.ErrorIs(t, m.purgeChunksJob(ctx, job), ErrUnavailable)
	m.events = publisher
	count, err := query.BindChunks(db, m.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.Positive(t, count)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, m.purgeChunksJob(canceled, job), context.Canceled)
	require.Error(t, m.purgeChunksJob(ctx, jobs.Job{}))
	require.NoError(t, m.purgeChunksJob(ctx, job))
	count, err = query.BindChunks(db, m.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
