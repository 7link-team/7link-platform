package events

import (
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func registerTestSubscriber(t *testing.T, m *Module, s Subscriber) {
	t.Helper()
	m.sealed = false
	require.NoError(t, m.AddSubscribers("test", s))
	require.Equal(t, "test", m.subscribers[s.Name].Module)
	m.sealed = true
}

func TestIdleSubscriberDoesNotAcquireWriterLease(t *testing.T) {
	m, _ := newTestSequencer(t)
	registerTestSubscriber(t, m, Subscriber{Name: "test.idle", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	require.NoError(t, m.initializeSubscribers(t.Context()))
	var acquired int
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		acquired++
		return nil, errors.New("idle dispatch acquired a writer lease")
	}
	for range 5 {
		n, err := m.DispatchPartition(t.Context(), "test.idle", 0)
		require.NoError(t, err)
		require.Zero(t, n)
	}
	require.Zero(t, acquired)
}
func emitEvents(t *testing.T, m *Module, db *gorm.DB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			e := testEvent()
			e.SubjectVersion = int64(i)
			_, err := m.Publish(t.Context(), tx, e)
			return err
		}))
	}
	_, err := m.Sequence(t.Context())
	require.NoError(t, err)
}
func TestSubscriberCatalogValidatesAtomically(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	valid := Subscriber{Name: "test.projector", EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }}
	for _, mutate := range []func(*Subscriber){func(s *Subscriber) { s.Name = "other.projector" }, func(s *Subscriber) { s.Partitions = 17 }, func(s *Subscriber) { s.Mode = "bad" }, func(s *Subscriber) { s.StartFrom = "bad" }, func(s *Subscriber) { s.EventTypes = []string{"bad"} }, func(s *Subscriber) { s.Handle = nil }} {
		bad := valid
		mutate(&bad)
		require.Error(t, m.AddSubscribers("test", valid, bad))
		require.Empty(t, m.subscribers)
	}
	require.NoError(t, m.AddSubscribers("test", valid))
	require.Error(t, m.AddSubscribers("test", valid))
	m.sealed = true
	require.ErrorIs(t, m.AddSubscribers("test", valid), ErrCatalogSealed)
}
func TestTransactionalSubscriberAtomicEffectsReceiptsAndResume(t *testing.T) {
	m, db := newTestSequencer(t)
	require.NoError(t, db.Exec("CREATE TABLE subscriber_effects (id BIGINT PRIMARY KEY)").Error)
	var calls, committed atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.projector", Mode: Transactional, StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(ctx context.Context, tx *gorm.DB, e Envelope) error {
		require.NotNil(t, tx)
		calls.Add(1)
		return tx.WithContext(ctx).Exec("INSERT INTO subscriber_effects VALUES (?)", e.EventID).Error
	}, AfterCommit: func(ctx context.Context, e Envelope) {
		var n int64
		require.NoError(t, db.WithContext(ctx).Table("subscriber_effects").Where("id = ?", e.EventID).Count(&n).Error)
		require.EqualValues(t, 1, n)
		committed.Add(1)
	}})
	emitEvents(t, m, db, 4)
	n, err := m.DispatchPartition(t.Context(), "test.projector", 0)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.EqualValues(t, 4, calls.Load())
	require.EqualValues(t, 4, committed.Load())
	c := query.BindCursors(db, m.names)
	_, err = c.WithContext(t.Context()).Where(c.Consumer.Eq("test.projector")).UpdateSimple(c.LastSeq.Value(0))
	require.NoError(t, err)
	_, err = m.DispatchPartition(t.Context(), "test.projector", 0)
	require.NoError(t, err)
	require.EqualValues(t, 4, calls.Load())
	var rows []model.Receipt
	require.NoError(t, db.Table(m.names.Receipts.String()).Find(&rows).Error)
	require.Len(t, rows, 4)
	require.NoError(t, m.SetPaused(t.Context(), "test.projector", true))
	emitEvents(t, m, db, 1)
	n, err = m.DispatchPartition(t.Context(), "test.projector", 0)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, m.SetPaused(t.Context(), "test.projector", false))
	_, err = m.DispatchPartition(t.Context(), "test.projector", 0)
	require.NoError(t, err)
	require.EqualValues(t, 5, calls.Load())
}
func TestFailedSubscriberParksWithoutPartialWritesAndBlocksOnlyAggregate(t *testing.T) {
	m, db := newTestSequencer(t)
	require.NoError(t, db.Exec("CREATE TABLE rollback_effects (id BIGINT)").Error)
	var fail atomic.Bool
	var committedCallbacks atomic.Int64
	fail.Store(true)
	var calls atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.projector", Mode: Transactional, StartFrom: Earliest, EventTypes: []string{"project.created"}, Handle: func(ctx context.Context, tx *gorm.DB, e Envelope) error {
		calls.Add(1)
		require.NoError(t, AfterCommit(ctx, func() { committedCallbacks.Add(1) }))
		if e.Subject.ID == 2 && fail.Load() {
			require.NoError(t, tx.Exec("INSERT INTO rollback_effects VALUES (1)").Error)
			return errors.New("handler failed")
		}
		return nil
	}})
	emitEvents(t, m, db, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := testEvent()
		e.Subject.ID = 3
		e.ProjectID = 3
		_, err := m.Publish(t.Context(), tx, e)
		return err
	}))
	_, err := m.Sequence(t.Context())
	require.NoError(t, err)
	n, err := m.DispatchPartition(t.Context(), "test.projector", 0)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.EqualValues(t, 4, calls.Load())
	var effects int64
	require.NoError(t, db.Table("rollback_effects").Count(&effects).Error)
	require.Zero(t, effects)
	parked := query.BindParked(db, m.names)
	rows, err := parked.WithContext(t.Context()).Order(parked.StreamSeq).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "FAILED", rows[0].Reason)
	require.Zero(t, rows[0].Attempts, "parked attempts count automatic redelivery independently of initial in-process retries")
	require.Equal(t, "BLOCKED", rows[1].Reason)
	fail.Store(false)
	_, err = parked.WithContext(t.Context()).Where(parked.ID.Eq(rows[0].ID)).UpdateSimple(parked.NextAttemptAtMS.Value(m.now().Add(-time.Hour).UnixMilli()))
	require.NoError(t, err)
	require.NoError(t, m.Redeliver(t.Context()))
	require.NoError(t, m.Redeliver(t.Context()))
	rows, err = parked.WithContext(t.Context()).Order(parked.StreamSeq).Find()
	require.NoError(t, err)
	require.Equal(t, "DELIVERED", rows[0].State)
	require.Equal(t, "DELIVERED", rows[1].State)
	require.EqualValues(t, 6, calls.Load())
	require.EqualValues(t, 3, committedCallbacks.Load())
}
func TestAtLeastOnceHandlerOutsideTransactionDeduplicatesAfterCommit(t *testing.T) {
	m, db := newTestSequencer(t)
	var calls atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.external", Mode: AtLeastOnce, StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(ctx context.Context, tx *gorm.DB, e Envelope) error {
		require.Nil(t, tx)
		calls.Add(1)
		return db.WithContext(ctx).Exec("CREATE TABLE IF NOT EXISTS external_effect (id BIGINT)").Error
	}})
	emitEvents(t, m, db, 1)
	_, err := m.DispatchPartition(t.Context(), "test.external", 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	c := query.BindCursors(db, m.names)
	_, err = c.WithContext(t.Context()).Where(c.Consumer.Eq("test.external")).UpdateSimple(c.LastSeq.Value(0))
	require.NoError(t, err)
	_, err = m.DispatchPartition(t.Context(), "test.external", 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
}
func TestSubscriberLatestPartitionsAndEphemeralIsolation(t *testing.T) {
	m, db := newTestSequencer(t)
	emitEvents(t, m, db, 2)
	var mu sync.Mutex
	seen := map[guid.ID]int{}
	registerTestSubscriber(t, m, Subscriber{Name: "test.partitioned", StartFrom: Latest, Partitions: 4, EventTypes: []string{"project.*"}, Handle: func(_ context.Context, _ *gorm.DB, e Envelope) error {
		mu.Lock()
		defer mu.Unlock()
		seen[e.EventID]++
		return nil
	}})
	for p := 0; p < 4; p++ {
		_, err := m.DispatchPartition(t.Context(), "test.partitioned", p)
		require.NoError(t, err)
	}
	require.Empty(t, seen)
	for id := guid.ID(1); id <= 20; id++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			e := testEvent()
			e.Subject.ID = id
			_, err := m.Publish(t.Context(), tx, e)
			return err
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.PublishEphemeral(t.Context(), tx, testEvent()); return err }))
	_, err := m.Sequence(t.Context())
	require.NoError(t, err)
	for p := 0; p < 4; p++ {
		_, err = m.DispatchPartition(t.Context(), "test.partitioned", p)
		require.NoError(t, err)
	}
	require.Len(t, seen, 20)
	for _, n := range seen {
		require.Equal(t, 1, n)
	}
	_, err = m.DispatchPartition(t.Context(), "unknown", 0)
	require.ErrorIs(t, err, ErrSubscriberNotFound)
	_, err = m.DispatchPartition(t.Context(), "test.partitioned", 4)
	require.ErrorIs(t, err, ErrInvalidEvent)
}

func TestSubscriberCommitFailureRollsBackTransactionalAndReplaysExternalEffects(t *testing.T) {
	for _, mode := range []DeliveryMode{Transactional, AtLeastOnce} {
		t.Run(string(mode), func(t *testing.T) {
			m, db := newTestSequencer(t)
			require.NoError(t, db.Exec("CREATE TABLE durable_effects (id BIGINT PRIMARY KEY)").Error)
			var calls, callbacks atomic.Int64
			registerTestSubscriber(t, m, Subscriber{Name: "test.crash", Mode: mode, StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(ctx context.Context, tx *gorm.DB, e Envelope) error {
				calls.Add(1)
				require.NoError(t, AfterCommit(ctx, func() { callbacks.Add(1) }))
				if tx != nil {
					return tx.Exec("INSERT INTO durable_effects VALUES (?)", e.EventID).Error
				}
				return nil
			}})
			emitEvents(t, m, db, 1)
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_receipt", func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.Receipts.String() {
					tx.AddError(errors.New("receipt storage unavailable"))
				}
			}))
			_, err := m.DispatchPartition(t.Context(), "test.crash", 0)
			require.Error(t, err)
			require.EqualValues(t, 1, calls.Load())
			require.Zero(t, callbacks.Load())
			var n int64
			require.NoError(t, db.Table("durable_effects").Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, db.Callback().Create().Remove("fail_receipt"))
			_, err = m.DispatchPartition(t.Context(), "test.crash", 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, calls.Load())
			require.EqualValues(t, 1, callbacks.Load())
			_, err = m.DispatchPartition(t.Context(), "test.crash", 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, calls.Load())
		})
	}
	require.ErrorIs(t, AfterCommit(t.Context(), func() {}), ErrInvalidEvent)
}
func TestSubscriberLifecycleRunsAndQuiescesDurableConsumers(t *testing.T) {
	m, db := newTestSequencer(t)
	var calls atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.lifecycle", StartFrom: Earliest, Partitions: 2, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { calls.Add(1); return nil }})
	emitEvents(t, m, db, 2)
	require.NoError(t, m.Start(t.Context()))
	require.Eventually(t, func() bool { return calls.Load() == 2 }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Stop(t.Context()))
	before := calls.Load()
	emitEvents(t, m, db, 1)
	time.Sleep(250 * time.Millisecond)
	require.Equal(t, before, calls.Load())
}

func TestSubscriberFailuresCancelAndRemainRecoverable(t *testing.T) {
	m, db := newTestSequencer(t)
	var called atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.cancel", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(ctx context.Context, tx *gorm.DB, e Envelope) error { called.Add(1); return context.Canceled }})
	emitEvents(t, m, db, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.DispatchPartition(ctx, "test.cancel", 0)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, called.Load())
	old := m.acquire
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, dlock.ErrNotAcquired
	}
	n, err := m.DispatchPartition(t.Context(), "test.cancel", 0)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, m.Redeliver(t.Context()))
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, errors.New("lease storage failed")
	}
	_, err = m.DispatchPartition(t.Context(), "test.cancel", 0)
	require.ErrorContains(t, err, "lease storage failed")
	m.acquire = old
	m.ids = &testIDs{zero: true}
	_, err = m.DispatchPartition(t.Context(), "test.cancel", 0)
	require.ErrorIs(t, err, ErrUnavailable)
	m.ids = &testIDs{err: errors.New("id failure")}
	_, err = m.DispatchPartition(t.Context(), "test.cancel", 0)
	require.ErrorContains(t, err, "id failure")
	require.Len(t, []rune(truncateError(errors.New(strings.Repeat("?", 2100)))), 2000)
	e := testEvent()
	e.CausationID = 23
	row := model.EventRow{ID: 1, ProjectID: &e.ProjectID, ActorID: &e.Actor.ID, CausationID: &e.CausationID, Topics: "[]"}
	require.EqualValues(t, 23, envelope(&row).CausationID)
	require.False(t, matchesType(Subscriber{EventTypes: []string{"other.*"}}, "project.created"))
	panicSubscriber := Subscriber{Mode: AtLeastOnce, Handle: func(context.Context, *gorm.DB, Envelope) error { panic("private panic") }}
	require.ErrorContains(t, m.handle(t.Context(), nil, panicSubscriber, Envelope{}), "panicked")
	missing := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	require.ErrorIs(t, missing.Redeliver(t.Context()), ErrUnavailable)
	_, err = missing.Purge(t.Context())
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = missing.Subscribers(t.Context())
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, missing.SetPaused(t.Context(), "test.cancel", true), ErrUnavailable)
}
