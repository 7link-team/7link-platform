package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StreamEvent is the immutable, sequenced view consumed by realtime replicas.
// SIGNAL payloads are intentionally omitted at this boundary.
type StreamEvent struct {
	Seq                     int64
	ID, TenantID            guid.ID
	Type                    string
	Topics                  []string
	Mode                    RealtimeMode
	Data                    json.RawMessage
	OccurredAt, SequencedAt time.Time
}

type TailPage struct {
	Events    []StreamEvent
	HighWater int64
}

// Tail reads committed sequence order from the writer, avoiding replica lag.
// after=0 with newest=true warms a replica with the latest retained events.
type Tail func(context.Context, int64, int, bool) (TailPage, error)

// Wakeups coalesces PostgreSQL sequence notifications. Non-PG engines return
// a nil channel; consumers always retain their polling interval.
type Wakeups func(context.Context) (<-chan struct{}, error)

func (m *Module) Wakeups(ctx context.Context) (<-chan struct{}, error) {
	db := m.writer()
	if db == nil {
		return nil, ErrUnavailable
	}
	return query.Listen(ctx, db, m.names, true)
}

func (m *Module) writer() *gorm.DB {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}

// Sequence serializes the entire assignment transaction with a database mutex.
// Unlike a renewable lease this mutex cannot expire while a leader is writing;
// incrementing and CASing the durable fence additionally detects stale state.
func (m *Module) Sequence(ctx context.Context) (int, error) {
	db := m.writer()
	if db == nil || m.guard == nil || m.ids == nil {
		return 0, ErrUnavailable
	}
	q := query.BindWithNames(db, m.names)
	pending, err := q.WithContext(ctx).Where(q.StreamSeq.IsNull()).Limit(1).Find()
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	count := 0
	published := map[string]int{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "events:sequencer"); err != nil {
			return err
		}
		c := query.BindCursors(tx, m.names)
		cursor, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.Consumer.Eq("$sequencer"), c.PartitionNo.Eq(0)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			id, nextErr := m.ids.Next()
			if nextErr != nil {
				return nextErr
			}
			if id <= 0 {
				return ErrUnavailable
			}
			cursor = &model.Cursor{ID: guid.ID(id), Consumer: "$sequencer", UpdatedAt: m.now()}
			if err = c.WithContext(ctx).Create(cursor); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if cursor.Paused {
			return nil
		}
		q := query.BindWithNames(tx, m.names)
		rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.StreamSeq.IsNull()).Order(q.ID).Limit(500).Find()
		if err != nil || len(rows) == 0 {
			return err
		}
		if cursor.LastSeq > math.MaxInt64-int64(len(rows)) || cursor.FenceToken == math.MaxInt64 {
			return ErrInvalidEvent
		}
		// Keep each aggregate's positions in the batch, reorder only their rows.
		type aggregate struct {
			tenant guid.ID
			kind   string
			id     guid.ID
		}
		positions := map[aggregate][]int{}
		for i, r := range rows {
			key := aggregate{r.TenantID, r.SubjectKind, r.SubjectID}
			positions[key] = append(positions[key], i)
		}
		for _, indices := range positions {
			group := make([]*model.EventRow, len(indices))
			for i, pos := range indices {
				group[i] = rows[pos]
			}
			sort.SliceStable(group, func(i, j int) bool { return group[i].SubjectVersion < group[j].SubjectVersion })
			for i, pos := range indices {
				rows[pos] = group[i]
			}
		}
		now := m.now().UTC().Truncate(time.Millisecond)
		for i, row := range rows {
			result, updateErr := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.StreamSeq.IsNull()).UpdateSimple(q.StreamSeq.Value(cursor.LastSeq+int64(i)+1), q.SequencedAt.Value(now))
			if updateErr != nil {
				return updateErr
			}
			if result.RowsAffected != 1 {
				return ErrInvalidEvent
			}
		}
		result, err := c.WithContext(ctx).Where(c.Consumer.Eq("$sequencer"), c.PartitionNo.Eq(0), c.LastSeq.Eq(cursor.LastSeq), c.FenceToken.Eq(cursor.FenceToken)).UpdateSimple(c.LastSeq.Value(cursor.LastSeq+int64(len(rows))), c.FenceToken.Value(cursor.FenceToken+1), c.UpdatedAt.Value(now))
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrInvalidEvent
		}
		if err = query.NotifySequenced(ctx, tx, m.names); err != nil {
			return err
		}
		count = len(rows)
		for _, row := range rows {
			published[row.EventType]++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for eventType, n := range published {
		m.metrics.published.WithLabelValues(eventType).Add(float64(n))
	}
	return count, nil
}

func (m *Module) Tail(ctx context.Context, after int64, limit int, newest bool) (TailPage, error) {
	var page TailPage
	db := m.writer()
	if db == nil {
		return page, ErrUnavailable
	}
	if after < 0 || limit < 1 || limit > 10000 || newest && after != 0 {
		return page, ErrInvalidEvent
	}
	// Sequencing commits immutable rows and its high-water mark atomically.
	// Reading the high-water first and bounding the row read by it cannot see
	// uncommitted/future events, and does not need a writer-reserving transaction.
	err := func(tx *gorm.DB) error {
		c := query.BindCursors(tx, m.names)
		cursor, err := c.WithContext(ctx).Where(c.Consumer.Eq("$sequencer"), c.PartitionNo.Eq(0)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		page.HighWater = cursor.LastSeq
		q := query.BindWithNames(tx, m.names)
		scope := q.WithContext(ctx).Where(q.StreamSeq.Gt(after), q.StreamSeq.Lte(page.HighWater))
		if newest {
			scope = scope.Order(q.StreamSeq.Desc())
		} else {
			scope = scope.Order(q.StreamSeq)
		}
		rows, err := scope.Limit(limit).Find()
		if err != nil {
			return err
		}
		if newest {
			for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
		for _, r := range rows {
			if r.StreamSeq == nil || r.SequencedAt == nil {
				return ErrInvalidEvent
			}
			e := StreamEvent{Seq: *r.StreamSeq, ID: r.ID, TenantID: r.TenantID, Type: r.EventType, Mode: RealtimeMode(r.RealtimeMode), OccurredAt: r.OccurredAt, SequencedAt: *r.SequencedAt}
			if err = json.Unmarshal([]byte(r.Topics), &e.Topics); err != nil {
				return err
			}
			if e.Mode == RealtimeFull {
				e.Data = json.RawMessage(r.Data)
			}
			page.Events = append(page.Events, e)
		}
		return nil
	}(db.WithContext(ctx))
	return page, err
}

type sequencerRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Module) Stop(ctx context.Context) error { return m.Quiesce(ctx) }

func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run != nil {
		return nil
	}
	if !m.sealed || m.guard == nil {
		return ErrUnavailable
	}
	if m.writer() == nil {
		slog.WarnContext(ctx, "domain event sequencing unavailable without a writable datasource")
		return nil
	}
	if _, err := m.Sequence(ctx); err != nil {
		return err
	}
	if _, err := m.Tail(ctx, 0, 1, false); err != nil {
		return err
	}
	if err := m.initializeSubscribers(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	wake, err := query.Listen(runCtx, m.writer(), m.names, false)
	if err != nil {
		cancel()
		return err
	}
	run := &sequencerRun{cancel: cancel, done: make(chan struct{})}
	m.run = run
	var workers sync.WaitGroup
	for _, subscriber := range m.subscribers {
		for partition := 0; partition < subscriber.Partitions; partition++ {
			workers.Add(1)
			go func() { defer workers.Done(); m.subscriberLoop(runCtx, subscriber, partition) }()
		}
	}
	workers.Add(1)
	go func() { defer workers.Done(); m.maintenanceLoop(runCtx) }()
	go func() {
		defer close(run.done)
		defer workers.Wait()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			case _, open := <-wake:
				if !open {
					wake = nil
				}
			}
			if _, err := m.Sequence(runCtx); err != nil && runCtx.Err() == nil && !gormx.IsSQLiteBusy(err) {
				slog.ErrorContext(runCtx, "sequence domain events", "error", err)
			}
		}
	}()
	return nil
}
func (m *Module) Quiesce(ctx context.Context) error {
	m.mu.Lock()
	run := m.run
	m.mu.Unlock()
	if run == nil {
		return nil
	}
	run.cancel()
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
