package events

import (
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"hash/fnv"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type DeliveryMode string
type StartFrom string

const (
	Transactional DeliveryMode = "Transactional"
	AtLeastOnce   DeliveryMode = "AtLeastOnce"
	Latest        StartFrom    = "Latest"
	Earliest      StartFrom    = "Earliest"
)

type Envelope struct {
	EventID             guid.ID
	EventType           string
	SchemaVersion       int
	StreamSeq           int64
	TenantID, ProjectID guid.ID
	OccurredAt          time.Time
	Actor               Actor
	Subject             Subject
	SubjectVersion      int64
	Topics              []string
	Data                json.RawMessage
	CorrelationID       string
	CausationID         guid.ID
}
type Subscriber struct {
	Name        string
	EventTypes  []string
	Mode        DeliveryMode
	Partitions  int
	StartFrom   StartFrom
	Handle      func(context.Context, *gorm.DB, Envelope) error
	AfterCommit func(context.Context, Envelope)
}
type commitCallbacksKey struct{}

// AfterCommit schedules a realtime hint only after this subscriber transaction
// commits. Callbacks are discarded on rollback and must not retain transaction handles.
func AfterCommit(ctx context.Context, callback func()) error {
	callbacks, ok := ctx.Value(commitCallbacksKey{}).(*[]func())
	if !ok || callback == nil {
		return ErrInvalidEvent
	}
	*callbacks = append(*callbacks, callback)
	return nil
}

type SubscriberCatalog interface {
	AddSubscribers(string, ...Subscriber) error
}

func (m *Module) AddSubscribers(owner string, subscribers ...Subscriber) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if !kindPattern.MatchString(owner) {
		return ErrInvalidEvent
	}
	pending := map[string]Subscriber{}
	for _, s := range subscribers {
		if !eventPattern.MatchString(s.Name) || !strings.HasPrefix(s.Name, owner+".") || len(s.Name) > 96 || s.Handle == nil || len(s.EventTypes) == 0 {
			return ErrInvalidEvent
		}
		if _, ok := m.subscribers[s.Name]; ok {
			return ErrInvalidEvent
		}
		if _, ok := pending[s.Name]; ok {
			return ErrInvalidEvent
		}
		if s.Partitions == 0 {
			s.Partitions = 1
		}
		if s.Mode == "" {
			s.Mode = Transactional
		}
		if s.StartFrom == "" {
			s.StartFrom = Latest
		}
		if s.Partitions < 1 || s.Partitions > 16 || (s.Mode != Transactional && s.Mode != AtLeastOnce) || (s.StartFrom != Latest && s.StartFrom != Earliest) {
			return ErrInvalidEvent
		}
		s.EventTypes = append([]string(nil), s.EventTypes...)
		for _, pattern := range s.EventTypes {
			if !eventPattern.MatchString(pattern) && !(strings.HasSuffix(pattern, ".*") && kindPattern.MatchString(strings.TrimSuffix(pattern, ".*"))) {
				return ErrInvalidEvent
			}
		}
		pending[s.Name] = s
	}
	for name, s := range pending {
		m.subscribers[name] = s
	}
	return nil
}
func subscriberPartition(kind string, id guid.ID, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kind + ":" + id.String()))
	return int(h.Sum32() % uint32(n))
}
func matchesType(s Subscriber, eventType string) bool {
	for _, p := range s.EventTypes {
		if p == eventType || strings.HasSuffix(p, ".*") && strings.HasPrefix(eventType, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}
func envelope(r *model.EventRow) Envelope {
	e := Envelope{EventID: r.ID, EventType: r.EventType, SchemaVersion: r.SchemaVersion, TenantID: r.TenantID, OccurredAt: r.OccurredAt, Actor: Actor{Kind: r.ActorKind, AuthoritySource: r.AuthoritySource}, Subject: Subject{Kind: r.SubjectKind, ID: r.SubjectID}, SubjectVersion: r.SubjectVersion, Data: json.RawMessage(r.Data), CorrelationID: r.CorrelationID}
	if r.StreamSeq != nil {
		e.StreamSeq = *r.StreamSeq
	}
	if r.ProjectID != nil {
		e.ProjectID = *r.ProjectID
	}
	if r.ActorID != nil {
		e.Actor.ID = *r.ActorID
	}
	if r.CausationID != nil {
		e.CausationID = *r.CausationID
	}
	_ = json.Unmarshal([]byte(r.Topics), &e.Topics)
	return e
}
func (m *Module) newID() (guid.ID, error) {
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(id), nil
}
func (m *Module) subscriberCursor(ctx context.Context, tx *gorm.DB, s Subscriber, p int, name string) (*model.Cursor, error) {
	q := query.BindCursors(tx, m.names)
	c, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.Consumer.Eq(name), q.PartitionNo.Eq(p)).Take()
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return c, err
	}
	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	c = &model.Cursor{ID: id, Consumer: name, PartitionNo: p, UpdatedAt: m.now()}
	if s.StartFrom == Latest {
		events := query.BindWithNames(tx, m.names)
		last, e := events.WithContext(ctx).Where(events.StreamSeq.IsNotNull()).Order(events.StreamSeq.Desc()).Take()
		if e == nil {
			c.LastSeq = *last.StreamSeq
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, e
		}
	}
	if err = q.WithContext(ctx).Create(c); err != nil {
		return nil, err
	}
	return c, nil
}
func (m *Module) advance(ctx context.Context, tx *gorm.DB, c *model.Cursor, seq int64) error {
	q := query.BindCursors(tx, m.names)
	info, err := q.WithContext(ctx).Where(q.ID.Eq(c.ID), q.LastSeq.Eq(c.LastSeq), q.FenceToken.Eq(c.FenceToken), q.Paused.Is(false)).UpdateSimple(q.LastSeq.Value(seq), q.UpdatedAt.Value(m.now()))
	if err == nil && info.RowsAffected != 1 {
		return ErrVersionConflict
	}
	if err == nil {
		c.LastSeq = seq
	}
	return err
}
func (m *Module) receiptExists(ctx context.Context, tx *gorm.DB, name string, id guid.ID) (bool, error) {
	q := query.BindReceipts(tx, m.names)
	_, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.Consumer.Eq(name), q.EventID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) recordReceipt(ctx context.Context, tx *gorm.DB, name string, e Envelope) error {
	id, err := m.newID()
	if err != nil {
		return err
	}
	return query.BindReceipts(tx, m.names).WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Receipt{ID: id, Consumer: name, EventID: e.EventID, TenantID: e.TenantID, ProcessedAt: m.now()})
}
func (m *Module) blocked(ctx context.Context, tx *gorm.DB, name string, e Envelope) (bool, error) {
	q := query.BindParked(tx, m.names)
	_, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.Consumer.Eq(name), q.TenantID.Eq(e.TenantID), q.SubjectKind.Eq(e.Subject.Kind), q.SubjectID.Eq(e.Subject.ID), q.StreamSeq.Lt(e.StreamSeq), q.State.In("PARKED", "REDELIVERING")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) park(ctx context.Context, tx *gorm.DB, name string, p int, e Envelope, cause error) error {
	id, err := m.newID()
	if err != nil {
		return err
	}
	row := &model.Parked{ID: id, Consumer: name, PartitionNo: p, EventID: e.EventID, StreamSeq: e.StreamSeq, TenantID: e.TenantID, SubjectKind: e.Subject.Kind, SubjectID: e.Subject.ID, Reason: "BLOCKED", State: "PARKED", CreatedAt: m.now(), UpdatedAt: m.now(), Version: 1}
	if cause != nil {
		row.Reason = "FAILED"
		row.LastErrorCode = "handler_failed"
		row.LastError = truncateError(cause)
		due := m.now().Add(5 * time.Second).UnixMilli()
		row.NextAttemptAtMS = &due
	}
	if err := query.BindParked(tx, m.names).WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row); err != nil {
		return err
	}
	if cause != nil {
		return m.notifyParked(ctx, tx, name, e, id)
	}
	return nil
}
func truncateError(err error) string {
	s := err.Error()
	if utf8.RuneCountInString(s) > 2000 {
		s = string([]rune(s)[:2000])
	}
	return s
}
func (m *Module) handle(ctx context.Context, tx *gorm.DB, s Subscriber, e Envelope) (err error) {
	started := time.Now()
	defer func() {
		if recover() != nil {
			err = errors.New("events: subscriber handler panicked")
		}
		m.metrics.duration.WithLabelValues(s.Name).Observe(time.Since(started).Seconds())
		if err != nil {
			m.metrics.failures.WithLabelValues(s.Name).Inc()
		}
	}()
	handlerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.Mode == Transactional {
		err = tx.WithContext(handlerCtx).Transaction(func(nested *gorm.DB) error { return s.Handle(handlerCtx, nested, e) })
	} else {
		err = s.Handle(handlerCtx, nil, e)
	}
	if err == nil {
		err = handlerCtx.Err()
	}
	return err
}

// DispatchPartition owns one renewable partition lease; short database commits
// fence it before advancing. AtLeastOnce handlers receive no database transaction.
func (m *Module) DispatchPartition(ctx context.Context, name string, p int) (int, error) {
	m.mu.RLock()
	s, ok := m.subscribers[name]
	m.mu.RUnlock()
	var replay *model.Cursor
	if !ok && m.writer() != nil {
		cursors := query.BindCursors(m.writer(), m.names)
		candidate, err := cursors.WithContext(ctx).Where(cursors.Consumer.Eq(name), cursors.ReplayOf.IsNotNull()).Take()
		if err != nil {
			return 0, ErrSubscriberNotFound
		}
		replay = candidate
		m.mu.RLock()
		s, ok = m.subscribers[*replay.ReplayOf]
		m.mu.RUnlock()
		s.Partitions = 1
		s.StartFrom = Earliest
	}
	if !ok {
		return 0, ErrSubscriberNotFound
	}
	if p < 0 || p >= s.Partitions {
		return 0, ErrInvalidEvent
	}
	if m.writer() == nil || m.acquire == nil {
		return 0, ErrUnavailable
	}
	// The cursor is initialized at startup. An idle subscriber must not acquire
	// and release a database lease or advance its fence on every polling tick.
	// Replays retain their completion/parked-event handling below.
	if replay == nil {
		cursors := query.BindCursors(m.writer(), m.names)
		cursor, readErr := cursors.WithContext(ctx).Where(cursors.Consumer.Eq(name), cursors.PartitionNo.Eq(p)).Take()
		if readErr != nil && !errors.Is(readErr, gorm.ErrRecordNotFound) {
			return 0, readErr
		}
		if readErr == nil {
			if cursor.Paused {
				return 0, nil
			}
			events := query.BindWithNames(m.writer(), m.names)
			pending, readErr := events.WithContext(ctx).Where(events.StreamSeq.Gt(cursor.LastSeq)).Limit(1).Find()
			if readErr != nil || len(pending) == 0 {
				return 0, readErr
			}
		}
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lock, err := m.acquire(leaseCtx, fmt.Sprintf("events:sub:%s:%d", name, p), 30*time.Second, func(string, error) { cancel() })
	if errors.Is(err, dlock.ErrNotAcquired) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() {
		releaseCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = lock.Unlock(releaseCtx)
	}()
	var c *model.Cursor
	err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
		if e := lock.FenceOn(leaseCtx, tx); e != nil {
			return e
		}
		var e error
		c, e = m.subscriberCursor(leaseCtx, tx, s, p, name)
		if e != nil || c.Paused {
			return e
		}
		if c.FenceToken == math.MaxInt64 {
			return ErrVersionConflict
		}
		cursors := query.BindCursors(tx, m.names)
		info, e := cursors.WithContext(leaseCtx).Where(cursors.ID.Eq(c.ID), cursors.LastSeq.Eq(c.LastSeq), cursors.FenceToken.Eq(c.FenceToken)).UpdateSimple(cursors.FenceToken.Add(1), cursors.UpdatedAt.Value(m.now()))
		if e != nil {
			return e
		}
		if info.RowsAffected != 1 {
			return ErrVersionConflict
		}
		c.FenceToken++
		return nil
	})
	if err != nil || c.Paused {
		return 0, err
	}
	q := query.BindWithNames(m.writer(), m.names)
	reader := q.WithContext(leaseCtx).Where(q.StreamSeq.Gt(c.LastSeq))
	if replay != nil {
		reader = reader.Where(q.StreamSeq.Lte(*replay.ReplayToSeq))
	}
	rows, err := reader.Order(q.StreamSeq).Limit(100).Find()
	if err != nil {
		return 0, err
	}
	if replay != nil && len(rows) == 0 {
		parked := query.BindParked(m.writer(), m.names)
		unresolved, x := parked.WithContext(leaseCtx).Where(parked.Consumer.Eq(name), parked.State.In("PARKED", "REDELIVERING")).Count()
		if x != nil {
			return 0, x
		}
		if unresolved > 0 {
			return 0, nil
		}
		err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
			if x := lock.FenceOn(leaseCtx, tx); x != nil {
				return x
			}
			cursors := query.BindCursors(tx, m.names)
			_, x := cursors.WithContext(leaseCtx).Where(cursors.ID.Eq(c.ID)).Delete()
			return x
		})
		return 0, err
	}
	count := 0
	for _, row := range rows {
		e := envelope(row)
		var callbacks []func()
		eventCtx := context.WithValue(leaseCtx, commitCallbacksKey{}, &callbacks)
		eligible := row.Durable && matchesType(s, e.EventType) && subscriberPartition(e.Subject.Kind, e.Subject.ID, s.Partitions) == p
		var handlerErr error
		handled := false
		for attempt := 0; attempt < 3; attempt++ {
			callbacks = nil
			if leaseCtx.Err() != nil {
				return count, leaseCtx.Err()
			}
			err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
				if fenceErr := lock.FenceOn(leaseCtx, tx); fenceErr != nil {
					return fenceErr
				}
				if !eligible {
					return m.advance(leaseCtx, tx, c, e.StreamSeq)
				}
				exists, x := m.receiptExists(leaseCtx, tx, s.Name, e.EventID)
				if x != nil {
					return x
				}
				if exists && (replay == nil || !replay.ReplayIgnoreReceipts) {
					return m.advance(leaseCtx, tx, c, e.StreamSeq)
				}
				blocked, x := m.blocked(leaseCtx, tx, name, e)
				if x != nil {
					return x
				}
				if blocked {
					if x = m.park(leaseCtx, tx, name, p, e, nil); x != nil {
						return x
					}
					return m.advance(leaseCtx, tx, c, e.StreamSeq)
				}
				if s.Mode == AtLeastOnce {
					return nil
				}
				handlerErr = m.handle(eventCtx, tx, s, e)
				handled = handlerErr == nil
				if handlerErr != nil {
					return handlerErr
				}
				if x = m.recordReceipt(leaseCtx, tx, s.Name, e); x != nil {
					return x
				}
				return m.advance(leaseCtx, tx, c, e.StreamSeq)
			})
			if err == nil && eligible && s.Mode == AtLeastOnce && c.LastSeq < e.StreamSeq {
				handlerErr = m.handle(eventCtx, nil, s, e)
				handled = handlerErr == nil
				if handlerErr == nil {
					err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
						if x := lock.FenceOn(leaseCtx, tx); x != nil {
							return x
						}
						if x := m.recordReceipt(leaseCtx, tx, s.Name, e); x != nil {
							return x
						}
						return m.advance(leaseCtx, tx, c, e.StreamSeq)
					})
				} else {
					err = handlerErr
				}
			}
			if err == nil {
				break
			}
			if handlerErr == nil {
				return count, err
			}
			if attempt < 2 {
				timer := time.NewTimer(time.Second * time.Duration(1<<attempt))
				select {
				case <-leaseCtx.Done():
					timer.Stop()
					return count, leaseCtx.Err()
				case <-timer.C:
				}
			}
		}
		if err != nil {
			if leaseCtx.Err() != nil {
				return count, leaseCtx.Err()
			}
			err = m.writer().WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
				if x := lock.FenceOn(leaseCtx, tx); x != nil {
					return x
				}
				if x := m.park(leaseCtx, tx, name, p, e, handlerErr); x != nil {
					return x
				}
				return m.advance(leaseCtx, tx, c, e.StreamSeq)
			})
			if err != nil {
				return count, err
			}
			m.metrics.parked.WithLabelValues(s.Name).Inc()
		}
		if handled && err == nil {
			for _, callback := range callbacks {
				callback()
			}
		}
		if handled && err == nil && s.AfterCommit != nil {
			s.AfterCommit(leaseCtx, e)
		}
		count++
	}
	return count, nil
}
func (m *Module) subscriberLoop(ctx context.Context, s Subscriber, p int) {
	delay := 200 * time.Millisecond
	for {
		count, err := m.DispatchPartition(ctx, s.Name, p)
		if err != nil && ctx.Err() == nil && !gormx.IsSQLiteBusy(err) {
			slog.ErrorContext(ctx, "dispatch domain events", "subscriber", s.Name, "partition", p, "error", err)
		}
		if count == 0 || err != nil {
			delay = min(2*delay, 2*time.Second)
		} else {
			delay = 200 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Module) initializeSubscribers(ctx context.Context) error {
	return m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "events:subscribers:init"); err != nil {
			return err
		}
		for _, s := range m.subscribers {
			for p := 0; p < s.Partitions; p++ {
				if _, err := m.subscriberCursor(ctx, tx, s, p, s.Name); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
