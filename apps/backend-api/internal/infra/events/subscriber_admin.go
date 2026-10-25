package events

import (
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"unicode/utf8"
)

var (
	ErrSubscriberNotFound = errors.New("events: subscriber not found")
	ErrParkedNotFound     = errors.New("events: parked event not found")
	ErrVersionConflict    = errors.New("events: version conflict")
	ErrParkedConflict     = errors.New("events: parked event state conflict")
	ErrReplayRange        = errors.New("events: replay outside retained range")
	ErrReplayActive       = errors.New("events: replay already active")
)

type CursorStatus struct {
	Partition    int
	LastSeq, Lag int64
}
type SubscriberStatus struct {
	Name            string
	Mode            DeliveryMode
	Partitions      int
	Cursors         []CursorStatus
	Parked, Blocked int64
	Paused          bool
}
type ReplayStatus struct {
	ReplayCursor   string
	FromSeq, ToSeq int64
}

func (m *Module) Subscribers(ctx context.Context) ([]SubscriberStatus, error) {
	if m.writer() == nil {
		return nil, ErrUnavailable
	}
	return m.SubscribersOn(ctx, m.writer())
}
func (m *Module) SubscribersOn(ctx context.Context, tx *gorm.DB) ([]SubscriberStatus, error) {
	if tx == nil {
		return nil, ErrUnavailable
	}
	m.mu.RLock()
	subs := make([]Subscriber, 0, len(m.subscribers))
	for _, s := range m.subscribers {
		subs = append(subs, s)
	}
	m.mu.RUnlock()
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
	events := query.BindWithNames(tx, m.names)
	highWater := int64(0)
	last, err := events.WithContext(ctx).Where(events.StreamSeq.IsNotNull()).Order(events.StreamSeq.Desc()).Take()
	if err == nil {
		highWater = *last.StreamSeq
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	result := make([]SubscriberStatus, 0, len(subs))
	for _, s := range subs {
		status := SubscriberStatus{Name: s.Name, Mode: s.Mode, Partitions: s.Partitions, Cursors: []CursorStatus{}}
		c := query.BindCursors(tx, m.names)
		rows, e := c.WithContext(ctx).Where(c.Consumer.Eq(s.Name)).Order(c.PartitionNo).Find()
		if e != nil {
			return nil, e
		}
		for p := 0; p < s.Partitions; p++ {
			seq := int64(0)
			if s.StartFrom == Latest {
				seq = highWater
			}
			for _, row := range rows {
				if row.PartitionNo == p {
					seq = row.LastSeq
					status.Paused = status.Paused || row.Paused
					break
				}
			}
			status.Cursors = append(status.Cursors, CursorStatus{Partition: p, LastSeq: seq, Lag: highWater - seq})
		}
		parked := query.BindParked(tx, m.names)
		status.Parked, e = parked.WithContext(ctx).Where(parked.Consumer.Eq(s.Name), parked.Reason.Eq("FAILED"), parked.State.In("PARKED", "REDELIVERING")).Count()
		if e != nil {
			return nil, e
		}
		status.Blocked, e = parked.WithContext(ctx).Where(parked.Consumer.Eq(s.Name), parked.Reason.Eq("BLOCKED"), parked.State.In("PARKED", "REDELIVERING")).Count()
		if e != nil {
			return nil, e
		}
		result = append(result, status)
	}
	return result, nil
}
func (m *Module) SetPausedOn(ctx context.Context, tx *gorm.DB, name string, paused bool) error {
	m.mu.RLock()
	s, ok := m.subscribers[name]
	m.mu.RUnlock()
	if !ok {
		return ErrSubscriberNotFound
	}
	if tx == nil || m.guard == nil {
		return ErrUnavailable
	}
	for p := 0; p < s.Partitions; p++ {
		if err := m.guard(ctx, tx, "events:cursor:"+name); err != nil {
			return err
		}
		c, err := m.subscriberCursor(ctx, tx, s, p, name)
		if err != nil {
			return err
		}
		q := query.BindCursors(tx, m.names)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(c.ID)).UpdateSimple(q.Paused.Value(paused), q.UpdatedAt.Value(m.now()))
		if err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) RetryParkedOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, principal guid.ID, reason string) (*model.Parked, error) {
	return m.resolveParked(ctx, tx, id, version, principal, reason, false)
}
func (m *Module) DiscardParkedOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, principal guid.ID, reason string) (*model.Parked, error) {
	return m.resolveParked(ctx, tx, id, version, principal, reason, true)
}
func (m *Module) resolveParked(ctx context.Context, tx *gorm.DB, id guid.ID, version int, principal guid.ID, reason string, discard bool) (*model.Parked, error) {
	if tx == nil {
		return nil, ErrUnavailable
	}
	if id <= 0 || version <= 0 || principal <= 0 || reason == "" || utf8.RuneCountInString(reason) > 2000 {
		return nil, ErrInvalidEvent
	}
	q := query.BindParked(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrParkedNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.Version != version {
		return nil, ErrVersionConflict
	}
	if row.State != "PARKED" {
		return nil, ErrParkedConflict
	}
	now := m.now()
	row.ResolvedBy = &principal
	row.ResolutionNote = reason
	row.UpdatedAt = now
	row.Version++
	if discard {
		row.State = "DISCARDED"
		row.NextAttemptAtMS = nil
		row.ResolvedAt = &now
	} else {
		blocked, e := m.blocked(ctx, tx, row.Consumer, Envelope{TenantID: row.TenantID, Subject: Subject{Kind: row.SubjectKind, ID: row.SubjectID}, StreamSeq: row.StreamSeq})
		if e != nil {
			return nil, e
		}
		if blocked {
			return nil, ErrParkedConflict
		}
		due := now.UnixMilli()
		row.NextAttemptAtMS = &due
		row.Attempts = 0
	}
	info, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(version), q.State.Eq("PARKED")).Select(q.State, q.Attempts, q.NextAttemptAtMS, q.ResolvedBy, q.ResolutionNote, q.UpdatedAt, q.ResolvedAt, q.Version).Updates(row)
	if err != nil {
		return nil, err
	}
	if info.RowsAffected != 1 {
		return nil, ErrVersionConflict
	}
	if discard {
		if err = m.releaseBlocked(ctx, tx, row); err != nil {
			return nil, err
		}
	}
	return row, nil
}
func (m *Module) releaseBlocked(ctx context.Context, tx *gorm.DB, resolved *model.Parked) error {
	q := query.BindParked(tx, m.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.Consumer.Eq(resolved.Consumer), q.TenantID.Eq(resolved.TenantID), q.SubjectKind.Eq(resolved.SubjectKind), q.SubjectID.Eq(resolved.SubjectID), q.State.Eq("PARKED"), q.Reason.Eq("BLOCKED"), q.StreamSeq.Gt(resolved.StreamSeq)).Order(q.StreamSeq).Limit(1).Find()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	next := rows[0]
	blocked, err := m.blocked(ctx, tx, next.Consumer, Envelope{TenantID: next.TenantID, Subject: Subject{Kind: next.SubjectKind, ID: next.SubjectID}, StreamSeq: next.StreamSeq})
	if err != nil || blocked {
		return err
	}
	_, err = q.WithContext(ctx).Where(q.ID.Eq(next.ID), q.Version.Eq(next.Version)).UpdateSimple(q.NextAttemptAtMS.Value(m.now().UnixMilli()), q.UpdatedAt.Value(m.now()), q.Version.Add(1))
	return err
}
func (m *Module) ReplayOn(ctx context.Context, tx *gorm.DB, name string, from, to int64, ignore bool) (ReplayStatus, error) {
	m.mu.RLock()
	_, ok := m.subscribers[name]
	m.mu.RUnlock()
	if !ok {
		return ReplayStatus{}, ErrSubscriberNotFound
	}
	if tx == nil || m.guard == nil {
		return ReplayStatus{}, ErrUnavailable
	}
	if err := m.guard(ctx, tx, "events:purge"); err != nil {
		return ReplayStatus{}, err
	}
	if err := m.guard(ctx, tx, "events:replay:"+name); err != nil {
		return ReplayStatus{}, err
	}
	c := query.BindCursors(tx, m.names)
	active, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.ReplayOf.Eq(name)).Find()
	if err != nil {
		return ReplayStatus{}, err
	}
	if len(active) > 0 {
		return ReplayStatus{}, ErrReplayActive
	}
	q := query.BindWithNames(tx, m.names)
	first, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.StreamSeq.IsNotNull()).Order(q.StreamSeq).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReplayStatus{}, ErrReplayRange
	}
	if err != nil {
		return ReplayStatus{}, err
	}
	last, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.StreamSeq.IsNotNull()).Order(q.StreamSeq.Desc()).Take()
	if err != nil {
		return ReplayStatus{}, err
	}
	if to == 0 {
		to = *last.StreamSeq
	}
	if from < *first.StreamSeq || from > to || to > *last.StreamSeq {
		return ReplayStatus{}, ErrReplayRange
	}
	id, err := m.newID()
	if err != nil {
		return ReplayStatus{}, err
	}
	cursor := name + "#replay-" + id.String()
	row := &model.Cursor{ID: id, Consumer: cursor, LastSeq: from - 1, ReplayOf: &name, ReplayToSeq: &to, ReplayIgnoreReceipts: ignore, UpdatedAt: m.now()}
	if err = c.WithContext(ctx).Create(row); err != nil {
		return ReplayStatus{}, err
	}
	return ReplayStatus{ReplayCursor: cursor, FromSeq: from, ToSeq: to}, nil
}

// SetPaused supplies the same operation for internal tooling.
func (m *Module) SetPaused(ctx context.Context, name string, paused bool) error {
	if m.writer() == nil {
		return ErrUnavailable
	}
	return m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.SetPausedOn(ctx, tx, name, paused) })
}
