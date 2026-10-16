package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// nextScheduleTime returns the next instant strictly after the database time.
// Cron calendar evaluation uses the declaration's zone, never the host zone.
func nextScheduleTime(s Schedule, afterMS int64) (int64, error) {
	if s.Interval > 0 {
		interval := s.Interval.Milliseconds()
		if interval < 1000 || afterMS > math.MaxInt64-interval {
			return 0, ErrInvalidDeclaration
		}
		return afterMS + interval, nil
	}
	zone, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return 0, err
	}
	parsed, err := cronParser.Parse(s.Cron)
	if err != nil {
		return 0, err
	}
	next := parsed.Next(time.UnixMilli(afterMS).In(zone))
	if next.IsZero() || next.UnixMilli() <= afterMS {
		return 0, ErrInvalidDeclaration
	}
	return next.UnixMilli(), nil
}

func scheduleDefinition(s Schedule, queue string, nowMS int64) (model.Schedule, error) {
	payload, err := encodePayload(s.Payload)
	if err != nil {
		return model.Schedule{}, err
	}
	definition, err := json.Marshal(struct {
		Schedule Schedule
		Queue    string
	}{s, queue})
	if err != nil {
		return model.Schedule{}, err
	}
	next, err := nextScheduleTime(s, nowMS)
	if err != nil {
		return model.Schedule{}, err
	}
	row := model.Schedule{Name: s.Name, Kind: s.Kind, Queue: queue, TenantScope: string(s.TenantScope), Timezone: s.Timezone, Payload: string(payload), Enabled: true, MisfirePolicy: string(s.Misfire), NextRunAtMS: next, DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(definition)), Version: 1, CreatedAt: time.UnixMilli(nowMS).UTC(), UpdatedAt: time.UnixMilli(nowMS).UTC()}
	if s.Interval > 0 {
		interval := s.Interval.Milliseconds()
		row.IntervalMS = &interval
	} else {
		cron := s.Cron
		row.Cron = &cron
	}
	return row, nil
}

// reconcileSchedules runs when a scheduler wins leadership. The transaction
// guard serializes overlapping leaders, while unchanged definitions retain
// durable progress and operator pauses. Removed declarations are disabled,
// never deleted; reintroducing one does not silently undo an operator's pause.
func (m *Module) reconcileSchedules(ctx context.Context) error {
	m.mu.RLock()
	if !m.sealed {
		m.mu.RUnlock()
		return ErrUnavailable
	}
	declarations := make([]Schedule, 0, len(m.schedules))
	queues := make(map[string]string, len(m.schedules))
	for _, s := range m.schedules {
		h, ok := m.handlers[s.Kind]
		if !ok {
			m.mu.RUnlock()
			return ErrUnknownKind
		}
		declarations = append(declarations, s)
		queues[s.Name] = h.Queue
	}
	m.mu.RUnlock()
	slices.SortFunc(declarations, func(a, b Schedule) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	var orphaned []string
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "jobs:schedules"); err != nil {
			return err
		}
		now, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		table := query.BindSchedules(tx, m.names)
		reader := table.WithContext(ctx)
		if tx.Dialector.Name() != "sqlite" {
			reader = reader.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		rows, err := reader.Order(table.Name).Find()
		if err != nil {
			return err
		}
		existing := make(map[string]*model.Schedule, len(rows))
		for _, row := range rows {
			existing[row.Name] = row
		}
		for _, s := range declarations {
			desired, err := scheduleDefinition(s, queues[s.Name], now)
			if err != nil {
				return fmt.Errorf("schedule %s: %w", s.Name, err)
			}
			current, found := existing[s.Name]
			delete(existing, s.Name)
			if !found {
				id, err := m.ids.Next()
				if err != nil {
					return err
				}
				if id <= 0 {
					return ErrUnavailable
				}
				desired.ID = guid.ID(id)
				if err := table.WithContext(ctx).Create(&desired); err != nil {
					return err
				}
				continue
			}
			if current.DefinitionHash == desired.DefinitionHash {
				continue
			}
			updates := []field.AssignExpr{table.Kind.Value(desired.Kind), table.Queue.Value(desired.Queue), table.TenantScope.Value(desired.TenantScope), table.Timezone.Value(desired.Timezone), table.Payload.Value(desired.Payload), table.MisfirePolicy.Value(desired.MisfirePolicy), table.DefinitionHash.Value(desired.DefinitionHash), table.NextRunAtMS.Value(desired.NextRunAtMS), table.UpdatedAt.Value(desired.UpdatedAt), table.Version.Add(1)}
			if desired.IntervalMS != nil {
				updates = append(updates, table.IntervalMS.Value(*desired.IntervalMS), table.Cron.Null())
			} else {
				updates = append(updates, table.IntervalMS.Null(), table.Cron.Value(*desired.Cron))
			}
			result, err := table.WithContext(ctx).Where(table.ID.Eq(current.ID), table.Version.Eq(current.Version)).UpdateSimple(updates...)
			if err != nil {
				return err
			}
			if result.RowsAffected != 1 {
				return errors.New("jobs: schedule changed during reconciliation")
			}
		}
		for name, row := range existing {
			orphaned = append(orphaned, name)
			if !row.Enabled {
				continue
			}
			if _, err := table.WithContext(ctx).Where(table.ID.Eq(row.ID), table.Version.Eq(row.Version)).UpdateSimple(table.Enabled.Value(false), table.UpdatedAt.Value(time.UnixMilli(now).UTC()), table.Version.Add(1)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(orphaned)
	for _, name := range orphaned {
		slog.WarnContext(ctx, "background schedule orphaned; declaration removed", "schedule", name)
	}
	return nil
}

// scheduleFireKey keeps the specified readable key where it fits the queue
// contract. Long declaration names retain their full identity through a digest.
func scheduleFireKey(name string, fireMS int64) string {
	key := fmt.Sprintf("schedule:%s:%d", name, fireMS)
	if len(key) <= 128 {
		return key
	}
	return fmt.Sprintf("schedule-sha256:%x", sha256.Sum256([]byte(key)))
}

// fireSchedule atomically enqueues a due plan (a platform task or tenant
// dispatcher) and advances progress.
// The pending guard precedes the schedule guard, matching queue admission's
// lock order. A stale leader must match the persisted definition before firing.
func (m *Module) fireSchedule(ctx context.Context, id guid.ID) (bool, error) {
	if id <= 0 {
		return false, ErrInvalidRequest
	}
	fired := false
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "jobs:pending:"+guid.ID(0).String()); err != nil {
			return err
		}
		if err := m.guard(ctx, tx, "jobs:schedules"); err != nil {
			return err
		}
		table := query.BindSchedules(tx, m.names)
		reader := table.WithContext(ctx)
		if tx.Dialector.Name() != "sqlite" {
			reader = reader.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		current, err := reader.Where(table.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		if !current.Enabled || current.NextRunAtMS > now {
			return nil
		}
		m.mu.RLock()
		declaration, known := m.schedules[current.Name]
		handler := m.handlers[declaration.Kind]
		sealed := m.sealed
		m.mu.RUnlock()
		if !sealed {
			return ErrUnavailable
		}
		if !known {
			return nil
		}
		desired, err := scheduleDefinition(declaration, handler.Queue, now)
		if err != nil {
			return err
		}
		if desired.DefinitionHash != current.DefinitionHash {
			return nil
		}
		// Tenant fanout requires its registered handler and active-tenant provider;
		// never substitute a platform execution for a PER_TENANT declaration.
		if declaration.TenantScope == ScopePerTenant && m.activeTenants == nil {
			return ErrUnavailable
		}
		following, err := nextScheduleTime(declaration, current.NextRunAtMS)
		if err != nil {
			return err
		}
		// A slot is missed once its following occurrence is also due. Ordinary
		// scheduler polling latency within the slot must not suppress every SKIP plan.
		skipped := declaration.Misfire == MisfireSkip && following <= now
		updates := []field.AssignExpr{table.NextRunAtMS.Value(desired.NextRunAtMS), table.UpdatedAt.Value(time.UnixMilli(now).UTC()), table.Version.Add(1)}
		if !skipped {
			kind := current.Kind
			if declaration.TenantScope == ScopePerTenant {
				kind = fanoutKind
			}
			job, err := m.Enqueue(ctx, tx, EnqueueRequest{Kind: kind, Payload: json.RawMessage(current.Payload), IdempotencyKey: scheduleFireKey(current.Name, current.NextRunAtMS), ConcurrencyKey: current.Name})
			if err != nil {
				return err
			}
			if declaration.TenantScope == ScopePerTenant && job.Created {
				jobs := query.BindJobs(tx, m.names)
				result, err := jobs.WithContext(ctx).Where(jobs.ID.Eq(job.JobID), jobs.Kind.Eq(fanoutKind), jobs.FanoutKind.IsNull(), jobs.FanoutFireMS.IsNull()).UpdateSimple(jobs.FanoutKind.Value(current.Kind), jobs.FanoutFireMS.Value(current.NextRunAtMS))
				if err != nil {
					return err
				}
				if result.RowsAffected != 1 {
					return ErrInvalidRequest
				}
			}
			updates = append(updates, table.LastFiredAtMS.Value(current.NextRunAtMS), table.LastJobID.Value(job.JobID))
			fired = true
		}
		result, err := table.WithContext(ctx).Where(table.ID.Eq(id), table.Version.Eq(current.Version), table.Enabled.Is(true), table.NextRunAtMS.Eq(current.NextRunAtMS)).UpdateSimple(updates...)
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return errors.New("jobs: schedule changed during firing")
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return fired, nil
}

// fireDueSchedules bounds each scan and settles plans independently, so an
// invalid or quota-blocked plan cannot roll back unrelated successful plans.
func (m *Module) fireDueSchedules(ctx context.Context) (int, error) {
	m.mu.RLock()
	ready := m.sealed
	names := make([]string, 0, len(m.schedules))
	for name := range m.schedules {
		names = append(names, name)
	}
	m.mu.RUnlock()
	if !ready {
		return 0, ErrUnavailable
	}
	if len(names) == 0 {
		return 0, nil
	}
	var rows []*model.Schedule
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		now, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		table := query.BindSchedules(tx, m.names)
		rows, err = table.WithContext(ctx).Where(table.Enabled.Is(true), table.NextRunAtMS.Lte(now), table.Name.In(names...)).Order(table.NextRunAtMS, table.ID).Limit(100).Find()
		return err
	})
	if err != nil {
		return 0, err
	}
	fired := 0
	var failures error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return fired, errors.Join(failures, err)
		}
		changed, err := m.fireSchedule(ctx, row.ID)
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("schedule %s: %w", row.Name, err))
		}
		if changed {
			fired++
		}
	}
	return fired, failures
}

// runScheduler holds the shared renewable leadership lease. Definition sync
// happens on every leadership acquisition, retaining durable progress across
// restarts; cancellation stops polling and releases the lease through Singleton.
func (m *Module) runScheduler(ctx context.Context) {
	m.Run(ctx, "jobs:scheduler", func(ctx context.Context) error {
		if err := m.reconcileSchedules(ctx); err != nil {
			return err
		}
		ticker := time.NewTicker(m.config.PollInterval)
		defer ticker.Stop()
		for {
			if _, err := m.fireDueSchedules(ctx); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	})
}
