package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidRequest = errors.New("jobs: invalid request")
	ErrQueueFull      = errors.New("jobs: tenant pending limit reached")
)

// Queue participates in the caller's transaction, or opens one when tx is nil.
// Any error requires rollback of a supplied transaction, including batch errors.
// Acquire queue guards before other locks that may enqueue to the same tenants.
type Queue interface {
	Enqueue(context.Context, *gorm.DB, EnqueueRequest) (EnqueueResult, error)
	EnqueueBatch(context.Context, *gorm.DB, []EnqueueRequest) ([]EnqueueResult, error)
	Cancel(context.Context, *gorm.DB, guid.ID, string, string) (bool, error)
}
type EnqueueRequest struct {
	TenantID                       guid.ID
	Kind                           string
	Payload                        any
	IdempotencyKey, ConcurrencyKey string
	RunAt                          time.Time
	Delay                          time.Duration
	Priority                       int
	CreatedBy                      guid.ID
}
type EnqueueResult struct {
	JobID   guid.ID
	Created bool
}

type preparedJob struct {
	request EnqueueRequest
	handler Handler
	payload string
}

func (m *Module) prepare(req EnqueueRequest) (preparedJob, error) {
	if req.TenantID < 0 || req.CreatedBy < 0 || req.Delay < 0 || (!req.RunAt.IsZero() && req.Delay != 0) || req.Priority < 0 || req.Priority > 9 || !validKey(req.IdempotencyKey) || !validKey(req.ConcurrencyKey) {
		return preparedJob{}, ErrInvalidRequest
	}
	m.mu.RLock()
	h, ok := m.handlers[req.Kind]
	sealed := m.sealed
	m.mu.RUnlock()
	if !sealed {
		return preparedJob{}, ErrUnavailable
	}
	if !ok {
		return preparedJob{}, ErrUnknownKind
	}
	payload, err := encodePayload(req.Payload)
	if err != nil {
		return preparedJob{}, err
	}
	return preparedJob{request: req, handler: h, payload: string(payload)}, nil
}
func validKey(key string) bool { return utf8.ValidString(key) && utf8.RuneCountInString(key) <= 128 }
func (m *Module) queueTransaction(ctx context.Context, tx *gorm.DB, fn func(*gorm.DB) error) error {
	if m == nil || m.ids == nil || m.guard == nil || m.names.Jobs.IsZero() || m.config.MaxPendingPerTenant < 1 {
		return ErrUnavailable
	}
	if tx != nil {
		if tx.Statement == nil {
			return ErrInvalidRequest
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			return ErrInvalidRequest
		}
		return fn(tx.WithContext(ctx))
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return ErrUnavailable
	}
	return m.runtime.Database().Write().WithContext(ctx).Transaction(fn)
}
func (m *Module) Enqueue(ctx context.Context, tx *gorm.DB, req EnqueueRequest) (EnqueueResult, error) {
	results, err := m.EnqueueBatch(ctx, tx, []EnqueueRequest{req})
	if err != nil {
		return EnqueueResult{}, err
	}
	return results[0], nil
}
func (m *Module) EnqueueBatch(ctx context.Context, tx *gorm.DB, reqs []EnqueueRequest) ([]EnqueueResult, error) {
	if m == nil {
		return nil, ErrUnavailable
	}
	if len(reqs) > 500 {
		return nil, ErrInvalidRequest
	}
	prepared := make([]preparedJob, len(reqs))
	tenants := make([]guid.ID, 0, len(reqs))
	for i, req := range reqs {
		item, err := m.prepare(req)
		if err != nil {
			return nil, err
		}
		prepared[i] = item
		tenants = append(tenants, req.TenantID)
	}
	slices.Sort(tenants)
	tenants = slices.Compact(tenants)
	results := make([]EnqueueResult, 0, len(reqs))
	err := m.queueTransaction(ctx, tx, func(tx *gorm.DB) error {
		for _, tenant := range tenants {
			if err := m.guard(ctx, tx, "jobs:pending:"+tenant.String()); err != nil {
				return err
			}
		}
		for _, item := range prepared {
			result, err := m.enqueueOn(ctx, tx, item)
			if err != nil {
				return err
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}
func (m *Module) enqueueOn(ctx context.Context, tx *gorm.DB, item preparedJob) (EnqueueResult, error) {
	req, h := item.request, item.handler
	table := query.BindJobs(tx, m.names)
	read := table.WithContext(ctx)
	if tx.Dialector.Name() != "sqlite" {
		read = read.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if req.IdempotencyKey != "" {
		row, err := read.Where(table.TenantID.Eq(req.TenantID), table.Kind.Eq(req.Kind), table.IdempotencyKey.Eq(req.IdempotencyKey)).First()
		if err == nil {
			return EnqueueResult{JobID: row.ID}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return EnqueueResult{}, err
		}
	}
	// Locking reads see current committed rows even when a MySQL caller opened
	// an older REPEATABLE READ snapshot before acquiring the quota guard.
	var pending []struct{ ID guid.ID }
	pendingRead := table.WithContext(ctx)
	if tx.Dialector.Name() != "sqlite" {
		pendingRead = pendingRead.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := pendingRead.Select(table.ID).Where(table.TenantID.Eq(req.TenantID), table.State.Eq("PENDING")).Limit(m.config.MaxPendingPerTenant).Scan(&pending)
	if err != nil {
		return EnqueueResult{}, err
	}
	if len(pending) >= m.config.MaxPendingPerTenant {
		return EnqueueResult{}, ErrQueueFull
	}
	millis, err := gormx.DatabaseClockMillis(ctx, tx)
	if err != nil {
		return EnqueueResult{}, err
	}
	runAt := millis
	if !req.RunAt.IsZero() {
		runAt = req.RunAt.UnixMilli()
	} else {
		delay := req.Delay.Milliseconds()
		if req.Delay%time.Millisecond != 0 {
			delay++
		}
		runAt += delay
	}
	id, err := m.ids.Next()
	if err != nil {
		return EnqueueResult{}, err
	}
	priority := req.Priority
	if priority == 0 {
		priority = h.Priority
	}
	now := time.UnixMilli(millis).UTC()
	row := model.Job{ID: guid.ID(id), TenantID: req.TenantID, Kind: req.Kind, Queue: h.Queue, State: "PENDING", Priority: priority, Payload: item.payload, MaxAttempts: h.MaxAttempts, RunAtMS: runAt, TimeoutMS: h.Timeout.Milliseconds(), ErrorHistory: "[]", CreatedAt: now, UpdatedAt: now, Version: 1}
	if req.IdempotencyKey != "" {
		row.IdempotencyKey = &req.IdempotencyKey
	}
	if req.ConcurrencyKey != "" {
		row.ConcurrencyKey = &req.ConcurrencyKey
	}
	if req.CreatedBy != 0 {
		row.CreatedBy = &req.CreatedBy
	}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		row.CorrelationID = span.TraceID().String()
	}
	if err := table.WithContext(ctx).Create(&row); err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{JobID: row.ID, Created: true}, nil
}
func (m *Module) Cancel(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind, key string) (bool, error) {
	if tenant < 0 || key == "" || !validKey(key) || !jobNamePattern.MatchString(kind) || len(kind) > 128 {
		return false, ErrInvalidRequest
	}
	changed := false
	err := m.queueTransaction(ctx, tx, func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "jobs:pending:"+tenant.String()); err != nil {
			return err
		}
		millis, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		table := query.BindJobs(tx, m.names)
		now := time.UnixMilli(millis).UTC()
		result, err := table.WithContext(ctx).Where(table.TenantID.Eq(tenant), table.Kind.Eq(kind), table.IdempotencyKey.Eq(key), table.State.Eq("PENDING")).UpdateSimple(table.State.Value("CANCELLED"), table.FinishedAt.Value(now), table.UpdatedAt.Value(now), table.Version.Add(1)) //nolint:misspell // Spec 09 freezes the CANCELLED state spelling.
		changed = result.RowsAffected > 0
		return err
	})
	if err != nil {
		return false, fmt.Errorf("cancel queued job: %w", err)
	}
	return changed, nil
}
