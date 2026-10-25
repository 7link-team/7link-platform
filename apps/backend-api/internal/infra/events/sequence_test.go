package events

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func newTestSequencer(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	m, db := newTestPublisher(t)
	locks := dlock.NewModule(m.runtime)
	source := locks.MigrationSource()
	source.Prefix = "ev_"
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	s := module.NewServices()
	require.NoError(t, locks.ProvideServices(s))
	require.NoError(t, s.Seal())
	m.acquire, err = module.Resolve[dlock.TryRenewingLease](s)
	require.NoError(t, err)
	m.guard, err = module.Resolve[dlock.TransactionGuard](s)
	require.NoError(t, err)
	return m, db
}

func TestSequenceUnavailableIDsAndLifecycleErrors(t *testing.T) {
	missing := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	_, err := missing.Sequence(t.Context())
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = missing.Tail(t.Context(), 0, 1, false)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = missing.Wakeups(t.Context())
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, missing.Stop(t.Context()))
	require.ErrorIs(t, missing.Start(t.Context()), ErrUnavailable)
	missing.sealed = true
	missing.guard = func(context.Context, *gorm.DB, string) error { return nil }
	require.NoError(t, missing.Start(t.Context()), "no writer preserves degraded startup")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, missing.Start(ctx), context.Canceled)
	for _, source := range []*testIDs{{err: errors.New("ID failure")}, {zero: true}} {
		m, db := newTestSequencer(t)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
		m.ids = source
		_, err = m.Sequence(t.Context())
		require.Error(t, err)
	}
	m, db := newTestSequencer(t)
	wake, err := m.Wakeups(t.Context())
	require.NoError(t, err)
	require.Nil(t, wake)
	for _, input := range []struct {
		after  int64
		limit  int
		newest bool
	}{{-1, 1, false}, {0, 0, false}, {0, 10001, false}, {1, 1, true}} {
		_, err = m.Tail(t.Context(), input.after, input.limit, input.newest)
		require.ErrorIs(t, err, ErrInvalidEvent)
	}
	page, err := m.Tail(t.Context(), 0, 1, false)
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.NoError(t, db.Migrator().DropTable(m.names.Cursors.String()))
	_, err = m.Tail(t.Context(), 0, 1, false)
	require.Error(t, err)
	require.Error(t, m.Start(t.Context()))
}

func TestIdleSequenceAndTailNeverAcquireWriterTransaction(t *testing.T) {
	m, db := newTestSequencer(t)
	emitEvents(t, m, db, 1)
	// Saturate the shared writer admission. Both idle sequencing and Tail must
	// finish through ordinary WAL reads without waiting for this transaction.
	tx := db.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	n, err := m.Sequence(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	page, err := m.Tail(ctx, 0, 10, false)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.EqualValues(t, 1, page.HighWater)
}

func TestTailFullPayloadAndCorruptEnvelopeFailClosed(t *testing.T) {
	m, db := newTestSequencer(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	_, err := m.Sequence(t.Context())
	require.NoError(t, err)
	q := query.BindWithNames(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Gt(guid.ID(0))).UpdateSimple(q.RealtimeMode.Value("FULL"))
	require.NoError(t, err)
	page, err := m.Tail(t.Context(), 0, 1, false)
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"Project"}`, string(page.Events[0].Data))
	_, err = q.WithContext(t.Context()).Where(q.ID.Gt(guid.ID(0))).UpdateSimple(q.SequencedAt.Null())
	require.NoError(t, err)
	_, err = m.Tail(t.Context(), 0, 1, false)
	require.ErrorIs(t, err, ErrInvalidEvent)
	_, err = q.WithContext(t.Context()).Where(q.ID.Gt(guid.ID(0))).UpdateSimple(q.SequencedAt.Value(time.Now()), q.Topics.Value(`{}`))
	require.NoError(t, err)
	_, err = m.Tail(t.Context(), 0, 1, false)
	require.Error(t, err)
	require.NoError(t, db.Migrator().DropTable(m.names.Events.String()))
	_, err = m.Tail(t.Context(), 0, 1, false)
	require.Error(t, err)
}

func TestSequenceTransactionFailuresNeverAdvanceCursorOrLeavePartialNumbers(t *testing.T) {
	for _, failure := range []string{"guard", "cursor_read", "cursor_create", "event_read", "event_update", "cursor_update", "stale_fence"} {
		t.Run(failure, func(t *testing.T) {
			m, db := newTestSequencer(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
			abort := errors.New("injected storage failure")
			switch failure {
			case "guard":
				m.guard = func(context.Context, *gorm.DB, string) error { return abort }
			case "cursor_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reject", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Cursors.String() {
						_ = tx.AddError(abort)
					}
				}))
			case "cursor_create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Cursors.String() {
						_ = tx.AddError(abort)
					}
				}))
			case "event_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reject", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Events.String() {
						_ = tx.AddError(abort)
					}
				}))
			case "event_update", "cursor_update":
				table := m.names.Events.String()
				if failure == "cursor_update" {
					table = m.names.Cursors.String()
				}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						_ = tx.AddError(abort)
					}
				}))
			case "stale_fence":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Cursors.String() {
						tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Eq{Column: "fence_token", Value: 99}}})
					}
				}))
			}
			n, err := m.Sequence(t.Context())
			require.Error(t, err)
			require.Zero(t, n)
			require.NoError(t, db.Callback().Query().Remove("reject"))
			require.NoError(t, db.Callback().Create().Remove("reject"))
			require.NoError(t, db.Callback().Update().Remove("reject"))
			q := query.BindWithNames(db, m.names)
			remaining, err := q.WithContext(t.Context()).Where(q.StreamSeq.IsNull()).Count()
			require.NoError(t, err)
			require.EqualValues(t, 1, remaining)
			c := query.BindCursors(db, m.names)
			cursor, err := c.WithContext(t.Context()).Where(c.Consumer.Eq("$sequencer")).Take()
			if err == nil {
				require.Zero(t, cursor.LastSeq)
			} else {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			}
		})
	}
}

func TestSequenceConcurrentWritersAndReplicasHaveNoGaps(t *testing.T) {
	m, db := newTestSequencer(t)
	var writers sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		writers.Go(func() {
			failures <- db.Transaction(func(tx *gorm.DB) error {
				for j := 0; j < 50; j++ {
					if _, err := m.Publish(t.Context(), tx, testEvent()); err != nil {
						return err
					}
				}
				return nil
			})
		})
	}
	writers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var replicas sync.WaitGroup
	failures = make(chan error, 4)
	for i := 0; i < 4; i++ {
		replicas.Go(func() {
			for {
				n, err := m.Sequence(t.Context())
				if err != nil || n == 0 {
					failures <- err
					return
				}
			}
		})
	}
	replicas.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	page, err := m.Tail(t.Context(), 0, 10000, false)
	require.NoError(t, err)
	require.Len(t, page.Events, 1000)
	for i, e := range page.Events {
		require.EqualValues(t, i+1, e.Seq)
	}
	require.EqualValues(t, 1000, page.HighWater)
}

func TestSequenceCommittedOrderLateIDsAndAggregateVersions(t *testing.T) {
	m, db := newTestSequencer(t)
	// Simulate a slow writer: the low ID becomes visible after a higher ID was
	// already sequenced. Tail must use sequence numbers, never event IDs.
	var delayed model.EventRow
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	q := query.BindWithNames(db, m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	delayed = *rows[0]
	require.NoError(t, db.Table(m.names.Events.String()).Where("id = ?", delayed.ID).Delete(&model.EventRow{}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := testEvent()
		e.SubjectVersion = 2
		_, err := m.Publish(t.Context(), tx, e)
		return err
	}))
	n, err := m.Sequence(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, q.WithContext(t.Context()).Create(&delayed))
	n, err = m.Sequence(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	page, err := m.Tail(t.Context(), 1, 100, false)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, delayed.ID, page.Events[0].ID)
	require.EqualValues(t, 2, page.Events[0].Seq)
	require.Empty(t, page.Events[0].Data)
	// Two versions in the same batch occupy their original aggregate slots.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, v := range []int64{6, 5} {
			e := testEvent()
			e.SubjectVersion = v
			if _, err := m.Publish(t.Context(), tx, e); err != nil {
				return err
			}
		}
		return nil
	}))
	n, err = m.Sequence(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	rows, err = q.WithContext(t.Context()).Where(q.StreamSeq.Gt(2)).Order(q.StreamSeq).Find()
	require.NoError(t, err)
	require.EqualValues(t, 5, rows[0].SubjectVersion)
	require.EqualValues(t, 6, rows[1].SubjectVersion)
	latest, err := m.Tail(t.Context(), 0, 2, true)
	require.NoError(t, err)
	require.EqualValues(t, 4, latest.HighWater)
	require.EqualValues(t, 3, latest.Events[0].Seq)
}

func TestSequenceRollbackPausePurgeAndOverflow(t *testing.T) {
	m, db := newTestSequencer(t)
	abort := errors.New("business rollback")
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.Publish(t.Context(), tx, testEvent())
		require.NoError(t, err)
		return abort
	})
	require.ErrorIs(t, err, abort)
	n, err := m.Sequence(t.Context())
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	n, err = m.Sequence(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	q := query.BindWithNames(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.StreamSeq.IsNotNull()).Delete()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	n, err = m.Sequence(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	page, err := m.Tail(t.Context(), 0, 100, false)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Events[0].Seq)
	c := query.BindCursors(db, m.names)
	_, err = c.WithContext(t.Context()).Where(c.Consumer.Eq("$sequencer")).UpdateSimple(c.Paused.Value(true))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	n, err = m.Sequence(t.Context())
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = c.WithContext(t.Context()).Where(c.Consumer.Eq("$sequencer")).UpdateSimple(c.Paused.Value(false), c.LastSeq.Value(math.MaxInt64))
	require.NoError(t, err)
	_, err = m.Sequence(t.Context())
	require.ErrorIs(t, err, ErrInvalidEvent)
	remaining, err := q.WithContext(t.Context()).Where(q.StreamSeq.IsNull()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, remaining)
}

func TestSequencerLifecycleAssignsAndStops(t *testing.T) {
	m, db := newTestSequencer(t)
	require.NoError(t, m.Start(t.Context()))
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, m.Quiesce(context.Background())) })
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	require.Eventually(t, func() bool {
		page, err := m.Tail(t.Context(), 0, 10, false)
		return err == nil && len(page.Events) == 1
	}, time.Second, 20*time.Millisecond)
	require.NoError(t, m.Quiesce(t.Context()))
}
