package events

import (
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"log/slog"
	"time"
)

func (m *Module) maintenanceLoop(ctx context.Context) {
	redeliver := time.NewTicker(5 * time.Second)
	defer redeliver.Stop()
	replay := time.NewTicker(200 * time.Millisecond)
	defer replay.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-redeliver.C:
			if err := m.Redeliver(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "redeliver domain events", "error", err)
			}
		case <-replay.C:
			q := query.BindCursors(m.writer(), m.names)
			rows, err := q.WithContext(ctx).Where(q.ReplayOf.IsNotNull()).Find()
			if err == nil {
				for _, r := range rows {
					if _, err = m.DispatchPartition(ctx, r.Consumer, 0); err != nil && ctx.Err() == nil {
						slog.ErrorContext(ctx, "replay domain events", "error", err)
					}
				}
			}
		case <-purge.C:
			if _, err := m.Purge(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "purge domain events", "error", err)
			}
		}
	}
}
func (m *Module) Redeliver(ctx context.Context) error {
	if m.writer() == nil || m.acquire == nil {
		return ErrUnavailable
	}
	q := query.BindParked(m.writer(), m.names)
	rows, err := q.WithContext(ctx).Where(q.State.In("PARKED", "REDELIVERING"), q.NextAttemptAtMS.Lte(m.now().UnixMilli()), q.Attempts.Lt(10)).Order(q.StreamSeq).Limit(100).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = m.redeliverOne(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) redeliverOne(ctx context.Context, row *model.Parked) error {
	m.mu.RLock()
	s, ok := m.subscribers[row.Consumer]
	m.mu.RUnlock()
	ignoreReceipts := false
	if !ok {
		cursors := query.BindCursors(m.writer(), m.names)
		replay, err := cursors.WithContext(ctx).Where(cursors.Consumer.Eq(row.Consumer), cursors.ReplayOf.IsNotNull()).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		m.mu.RLock()
		s, ok = m.subscribers[*replay.ReplayOf]
		m.mu.RUnlock()
		if !ok {
			return nil
		}
		ignoreReceipts = replay.ReplayIgnoreReceipts
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lock, err := m.acquire(leaseCtx, fmt.Sprintf("events:sub:%s:%d", row.Consumer, row.PartitionNo), 30*time.Second, func(string, error) { cancel() })
	if errors.Is(err, dlock.ErrNotAcquired) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = lock.Unlock(releaseCtx)
	}()
	var event *model.EventRow
	claimed := false
	err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
		if x := lock.FenceOn(leaseCtx, tx); x != nil {
			return x
		}
		cursors := query.BindCursors(tx, m.names)
		cursor, x := cursors.WithContext(leaseCtx).Where(cursors.Consumer.Eq(row.Consumer), cursors.PartitionNo.Eq(row.PartitionNo)).Take()
		if x != nil {
			return x
		}
		if cursor.Paused {
			return nil
		}
		q := query.BindParked(tx, m.names)
		current, x := q.WithContext(leaseCtx).Where(q.ID.Eq(row.ID)).Take()
		if x != nil {
			return x
		}
		if current.Version != row.Version || current.NextAttemptAtMS == nil || *current.NextAttemptAtMS > m.now().UnixMilli() || (current.State != "PARKED" && current.State != "REDELIVERING") {
			return nil
		}
		blocked, x := m.blocked(leaseCtx, tx, row.Consumer, Envelope{TenantID: row.TenantID, Subject: Subject{Kind: row.SubjectKind, ID: row.SubjectID}, StreamSeq: row.StreamSeq})
		if x != nil || blocked {
			return x
		}
		events := query.BindWithNames(tx, m.names)
		event, x = events.WithContext(leaseCtx).Where(events.ID.Eq(row.EventID)).Take()
		if x != nil {
			return x
		}
		info, x := q.WithContext(leaseCtx).Where(q.ID.Eq(row.ID), q.Version.Eq(row.Version)).UpdateSimple(q.State.Value("REDELIVERING"), q.Attempts.Add(1), q.NextAttemptAtMS.Value(m.now().Add(30*time.Second).UnixMilli()), q.Version.Add(1), q.UpdatedAt.Value(m.now()))
		if x != nil {
			return x
		}
		claimed = info.RowsAffected == 1
		return nil
	})
	if err != nil || !claimed {
		return err
	}
	row.Version++
	row.Attempts++
	e := envelope(event)
	var callbacks []func()
	eventCtx := context.WithValue(leaseCtx, commitCallbacksKey{}, &callbacks)
	var handlerErr error
	if s.Mode == AtLeastOnce {
		exists, x := m.receiptExists(leaseCtx, m.writer(), s.Name, e.EventID)
		if x != nil {
			return x
		}
		if !exists || ignoreReceipts {
			handlerErr = m.handle(eventCtx, nil, s, e)
		}
	}
	err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
		if x := lock.FenceOn(leaseCtx, tx); x != nil {
			return x
		}
		if s.Mode == Transactional {
			exists, x := m.receiptExists(leaseCtx, tx, s.Name, e.EventID)
			if x != nil {
				return x
			}
			if !exists || ignoreReceipts {
				handlerErr = m.handle(eventCtx, tx, s, e)
			}
		}
		q := query.BindParked(tx, m.names)
		now := m.now()
		state := "DELIVERED"
		var due *int64
		var resolved *time.Time
		code, message := "", ""
		if handlerErr != nil {
			state = "PARKED"
			code = "handler_failed"
			message = truncateError(handlerErr)
			if row.Attempts < 10 {
				backoff := 5 * time.Second * time.Duration(1<<min(row.Attempts, 10))
				if backoff > time.Hour {
					backoff = time.Hour
				}
				next := now.Add(backoff).UnixMilli()
				due = &next
			}
		} else {
			if x := m.recordReceipt(leaseCtx, tx, s.Name, e); x != nil {
				return x
			}
			resolved = &now
		}
		info, x := q.WithContext(leaseCtx).Where(q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("REDELIVERING")).Select(q.State, q.NextAttemptAtMS, q.ResolvedAt, q.LastErrorCode, q.LastError, q.UpdatedAt, q.Version).Updates(&model.Parked{State: state, NextAttemptAtMS: due, ResolvedAt: resolved, LastErrorCode: code, LastError: message, UpdatedAt: now, Version: row.Version + 1})
		if x != nil {
			return x
		}
		if info.RowsAffected != 1 {
			return ErrVersionConflict
		}
		if handlerErr == nil {
			return m.releaseBlocked(leaseCtx, tx, row)
		}
		return nil
	})
	if err == nil && handlerErr == nil {
		for _, callback := range callbacks {
			callback()
		}
	}
	if err == nil && handlerErr == nil && s.AfterCommit != nil {
		s.AfterCommit(leaseCtx, e)
	}
	return err
}

// Purge removes only sequenced rows safe for every durable subscriber and every
// unresolved parked reference. Ephemeral transport has its own one-hour horizon.
func (m *Module) Purge(ctx context.Context) (int, error) {
	if m.writer() == nil || m.guard == nil {
		return 0, ErrUnavailable
	}
	count := 0
	err := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "events:purge"); err != nil {
			return err
		}
		cursors := query.BindCursors(tx, m.names)
		rows, err := cursors.WithContext(ctx).Find()
		if err != nil {
			return err
		}
		water := int64(0)
		for _, c := range rows {
			if c.Consumer == "$sequencer" {
				water = c.LastSeq
				break
			}
		}
		for _, c := range rows {
			if c.Consumer != "$sequencer" && c.LastSeq < water {
				water = c.LastSeq
			}
		}
		m.mu.RLock()
		subs := make([]Subscriber, 0, len(m.subscribers))
		for _, s := range m.subscribers {
			subs = append(subs, s)
		}
		m.mu.RUnlock()
		for _, s := range subs {
			for p := 0; p < s.Partitions; p++ {
				found := false
				for _, c := range rows {
					if c.Consumer == s.Name && c.PartitionNo == p {
						found = true
						break
					}
				}
				if !found && s.StartFrom == Earliest {
					water = 0
				}
			}
		}
		q := query.BindWithNames(tx, m.names)
		candidates, err := q.WithContext(ctx).Where(q.StreamSeq.IsNotNull()).Where(q.WithContext(ctx).Where(q.Durable.Is(false), q.OccurredAt.Lt(m.now().Add(-time.Hour))).Or(q.Durable.Is(true), q.OccurredAt.Lt(m.now().Add(-7*24*time.Hour)), q.StreamSeq.Lt(water))).Order(q.StreamSeq).Limit(1000).Find()
		if err != nil {
			return err
		}
		for _, row := range candidates {
			parked := query.BindParked(tx, m.names)
			n, x := parked.WithContext(ctx).Where(parked.EventID.Eq(row.ID), parked.State.In("PARKED", "REDELIVERING")).Count()
			if x != nil {
				return x
			}
			if n > 0 {
				continue
			}
			receipts := query.BindReceipts(tx, m.names)
			if _, x = receipts.WithContext(ctx).Where(receipts.EventID.Eq(row.ID)).Delete(); x != nil {
				return x
			}
			if _, x = parked.WithContext(ctx).Where(parked.EventID.Eq(row.ID), parked.State.In("DELIVERED", "DISCARDED")).Delete(); x != nil {
				return x
			}
			if _, x = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Delete(); x != nil {
				return x
			}
			count++
		}
		return nil
	})
	return count, err
}
