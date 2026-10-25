package events

import (
	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"sync/atomic"
	"testing"
	"time"
)

func TestPurgeHonorsAllCursorsParkedReferencesAndEphemeralHorizon(t *testing.T) {
	m, db := newTestSequencer(t)
	emitEvents(t, m, db, 3)
	q := query.BindWithNames(db, m.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.PublishEphemeral(t.Context(), tx, testEvent()); return err }))
	_, err := m.Sequence(t.Context())
	require.NoError(t, err)
	_, err = q.WithContext(t.Context()).Where(q.ID.Gt(guid.ID(0))).UpdateSimple(q.OccurredAt.Value(m.now().Add(-8 * 24 * time.Hour)))
	require.NoError(t, err)
	registerTestSubscriber(t, m, Subscriber{Name: "test.retention", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	n, err := m.Purge(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n) // only ephemeral; unseen Earliest pins durable rows
	_, err = m.DispatchPartition(t.Context(), "test.retention", 0)
	require.NoError(t, err)
	rows, err := q.WithContext(t.Context()).Order(q.StreamSeq).Find()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.park(t.Context(), tx, "test.retention", 0, envelope(rows[0]), context.DeadlineExceeded)
	}))
	n, err = m.Purge(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n) // seq1 parked; seq2 deleted; seq3 high-water boundary retained
	receipts := query.BindReceipts(db, m.names)
	count, err := receipts.WithContext(t.Context()).Where(receipts.EventID.Eq(rows[1].ID)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	parked := query.BindParked(db, m.names)
	item, err := parked.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, x := m.DiscardParkedOn(t.Context(), tx, item.ID, item.Version, 10, "resolved")
		return x
	}))
	n, err = m.Purge(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	// Never remove an old unsequenced row.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.PublishEphemeral(t.Context(), tx, testEvent()); return x }))
	_, err = q.WithContext(t.Context()).Where(q.StreamSeq.IsNull()).UpdateSimple(q.OccurredAt.Value(m.now().Add(-24 * time.Hour)))
	require.NoError(t, err)
	_, err = m.Purge(t.Context())
	require.NoError(t, err)
	count, err = q.WithContext(t.Context()).Where(q.StreamSeq.IsNull()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
func TestRedeliveryFailureBackoffAndManualCAS(t *testing.T) {
	m, db := newTestSequencer(t)
	emitEvents(t, m, db, 1)
	registerTestSubscriber(t, m, Subscriber{Name: "test.retry", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return context.DeadlineExceeded }})
	// Prepare parked directly: dispatch retry rollback behavior is tested separately.
	q := query.BindWithNames(db, m.names)
	row, err := q.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.NoError(t, m.SetPaused(t.Context(), "test.retry", false))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.park(t.Context(), tx, "test.retry", 0, envelope(row), context.DeadlineExceeded)
	}))
	p := query.BindParked(db, m.names)
	item, err := p.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, x := m.RetryParkedOn(t.Context(), tx, item.ID, item.Version, 10, "retry")
		return x
	}))
	require.NoError(t, m.Redeliver(t.Context()))
	item, err = p.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "PARKED", item.State)
	require.Equal(t, 1, item.Attempts)
	require.Greater(t, *item.NextAttemptAtMS, m.now().UnixMilli())
	require.NotEmpty(t, item.LastError)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, x := m.DiscardParkedOn(t.Context(), tx, item.ID, item.Version-1, 10, "discard")
		return x
	}), ErrVersionConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, x := m.DiscardParkedOn(t.Context(), tx, item.ID, item.Version, 10, "discard")
		return x
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, x := m.RetryParkedOn(t.Context(), tx, item.ID, item.Version+1, 10, "retry")
		return x
	}), ErrParkedConflict)
	var parked []model.Parked
	require.NoError(t, db.Table(m.names.Parked.String()).Find(&parked).Error)
	require.Len(t, parked, 1)
	require.Equal(t, "DISCARDED", parked[0].State)
}

func TestMaintenanceResumesDurableReplayAndRedeliversAfterRestart(t *testing.T) {
	m, db := newTestSequencer(t)
	var callbacks atomic.Int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.background", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(ctx context.Context, _ *gorm.DB, _ Envelope) error {
		return AfterCommit(ctx, func() { callbacks.Add(1) })
	}})
	emitEvents(t, m, db, 1)
	require.NoError(t, m.SetPaused(t.Context(), "test.background", false))
	c := query.BindCursors(db, m.names)
	_, err := c.WithContext(t.Context()).Where(c.Consumer.Eq("test.background")).UpdateSimple(c.LastSeq.Value(1))
	require.NoError(t, err)
	q := query.BindWithNames(db, m.names)
	event, err := q.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.park(t.Context(), tx, "test.background", 0, envelope(event), context.DeadlineExceeded)
	}))
	p := query.BindParked(db, m.names)
	_, err = p.WithContext(t.Context()).Where(p.Consumer.Eq("test.background")).UpdateSimple(p.NextAttemptAtMS.Value(m.now().UnixMilli()))
	require.NoError(t, err)
	var replay ReplayStatus
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var x error
		replay, x = m.ReplayOn(t.Context(), tx, "test.background", 1, 1, true)
		return x
	}))
	// Restart the owner: replay target/flags survive solely in persisted cursors.
	restarted := NewModule(m.runtime)
	restarted.ids = m.ids
	restarted.guard = m.guard
	restarted.acquire = m.acquire
	restarted.now = m.now
	require.NoError(t, restarted.AddSubscribers("test", m.subscribers["test.background"]))
	require.NoError(t, restarted.Initialize(t.Context()))
	require.NoError(t, restarted.Start(t.Context()))
	defer func() { require.NoError(t, restarted.Stop(t.Context())) }()
	require.Eventually(t, func() bool {
		n, x := c.WithContext(t.Context()).Where(c.Consumer.Eq(replay.ReplayCursor)).Count()
		return x == nil && n == 0
	}, 2*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		row, x := p.WithContext(t.Context()).Where(p.Consumer.Eq("test.background")).Take()
		return x == nil && row.State == "DELIVERED"
	}, 7*time.Second, 50*time.Millisecond)
	require.EqualValues(t, 1, callbacks.Load()) // replay receipt suppresses the original parked handler
}
