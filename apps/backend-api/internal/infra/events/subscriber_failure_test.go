package events

import (
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestSubscriberStorageFailuresNeverAdvanceCursorOrInvokeHandler(t *testing.T) {
	for _, target := range []string{"cursor", "events", "receipts", "parked"} {
		t.Run(target, func(t *testing.T) {
			m, db := newTestSequencer(t)
			calls := 0
			registerTestSubscriber(t, m, Subscriber{Name: "test.storage", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { calls++; return nil }})
			emitEvents(t, m, db, 1)
			require.NoError(t, m.SetPaused(t.Context(), "test.storage", false))
			table := map[string]string{"cursor": m.names.Cursors.String(), "events": m.names.Events.String(), "receipts": m.names.Receipts.String(), "parked": m.names.Parked.String()}[target]
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("query_unavailable", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("injected database failure"))
				}
			}))
			_, err := m.DispatchPartition(t.Context(), "test.storage", 0)
			require.ErrorContains(t, err, "injected database failure")
			require.Zero(t, calls)
			require.NoError(t, db.Callback().Query().Remove("query_unavailable"))
			c := query.BindCursors(db, m.names)
			cursor, err := c.WithContext(t.Context()).Where(c.Consumer.Eq("test.storage")).Take()
			require.NoError(t, err)
			require.Zero(t, cursor.LastSeq)
			_, err = m.DispatchPartition(t.Context(), "test.storage", 0)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
		})
	}
}
func TestPurgeFailureRollsBackReceiptsAndEventTogether(t *testing.T) {
	m, db := newTestSequencer(t)
	registerTestSubscriber(t, m, Subscriber{Name: "test.purge", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	emitEvents(t, m, db, 2)
	_, err := m.DispatchPartition(t.Context(), "test.purge", 0)
	require.NoError(t, err)
	q := query.BindWithNames(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Gt(guid.ID(0))).UpdateSimple(q.OccurredAt.Value(m.now().Add(-8 * 24 * time.Hour)))
	require.NoError(t, err)
	for _, target := range []string{m.names.Cursors.String(), m.names.Events.String(), m.names.Parked.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("purge_query_fail", func(tx *gorm.DB) {
			if tx.Statement.Table == target {
				tx.AddError(errors.New("purge query unavailable"))
			}
		}))
		_, err = m.Purge(t.Context())
		require.Error(t, err)
		require.NoError(t, db.Callback().Query().Remove("purge_query_fail"))
	}
	for _, target := range []string{m.names.Receipts.String(), m.names.Parked.String(), m.names.Events.String()} {
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("delete_unavailable", func(tx *gorm.DB) {
			if tx.Statement.Table == target {
				tx.AddError(errors.New("purge deletion unavailable"))
			}
		}))
		_, err = m.Purge(t.Context())
		require.ErrorContains(t, err, "purge deletion unavailable")
		require.NoError(t, db.Callback().Delete().Remove("delete_unavailable"))
		receipts := query.BindReceipts(db, m.names)
		n, x := receipts.WithContext(t.Context()).Count()
		require.NoError(t, x)
		require.EqualValues(t, 2, n)
		n, x = q.WithContext(t.Context()).Count()
		require.NoError(t, x)
		require.EqualValues(t, 2, n)
	}
	n, err := m.Purge(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
func TestSubscriberOperationsRejectInvalidInputsWithoutChanges(t *testing.T) {
	m, db := newTestSequencer(t)
	registerTestSubscriber(t, m, Subscriber{Name: "test.validation", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	_, err := m.SubscribersOn(t.Context(), nil)
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, m.SetPausedOn(t.Context(), nil, "test.validation", false), ErrUnavailable)
	_, err = m.ReplayOn(t.Context(), nil, "test.validation", 1, 2, false)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.ReplayOn(t.Context(), db, "missing", 1, 2, false)
	require.ErrorIs(t, err, ErrSubscriberNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.ReplayOn(t.Context(), tx, "test.validation", 1, 0, false); return x }), ErrReplayRange)
	for _, input := range []struct {
		id        guid.ID
		version   int
		principal guid.ID
		reason    string
	}{{0, 1, 1, "reason"}, {1, 0, 1, "reason"}, {1, 1, 0, "reason"}, {1, 1, 1, ""}} {
		_, err = m.RetryParkedOn(t.Context(), db, input.id, input.version, input.principal, input.reason)
		require.ErrorIs(t, err, ErrInvalidEvent)
	}
	_, err = m.RetryParkedOn(t.Context(), nil, 1, 1, 1, "reason")
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.DiscardParkedOn(t.Context(), db, 1, 1, 1, "reason")
	require.ErrorIs(t, err, ErrParkedNotFound)
	emitEvents(t, m, db, 1)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("cursor_query_fail", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Cursors.String() {
			tx.AddError(errors.New("cursor unavailable"))
		}
	}))
	require.Error(t, m.SetPaused(t.Context(), "test.validation", false))
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.ReplayOn(t.Context(), tx, "test.validation", 1, 0, false); return x }), "cursor unavailable")
	require.NoError(t, db.Callback().Query().Remove("cursor_query_fail"))
}

func TestSubscriberFencesRejectStaleOrExhaustedOwners(t *testing.T) {
	m, db := newTestSequencer(t)
	registerTestSubscriber(t, m, Subscriber{Name: "test.fence", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, Envelope) error { return nil }})
	emitEvents(t, m, db, 1)
	require.NoError(t, m.SetPaused(t.Context(), "test.fence", false))
	c := query.BindCursors(db, m.names)
	stale, err := c.WithContext(t.Context()).Where(c.Consumer.Eq("test.fence")).Take()
	require.NoError(t, err)
	_, err = c.WithContext(t.Context()).Where(c.ID.Eq(stale.ID)).UpdateSimple(c.FenceToken.Value(math.MaxInt64))
	require.NoError(t, err)
	_, err = m.DispatchPartition(t.Context(), "test.fence", 0)
	require.ErrorIs(t, err, ErrVersionConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.advance(t.Context(), tx, stale, 1) }), ErrVersionConflict)
	_, err = c.WithContext(t.Context()).Where(c.ID.Eq(stale.ID)).UpdateSimple(c.FenceToken.Value(1))
	require.NoError(t, err)
	_, err = m.DispatchPartition(t.Context(), "test.fence", 0)
	require.NoError(t, err)
	current, err := c.WithContext(t.Context()).Where(c.ID.Eq(stale.ID)).Take()
	require.NoError(t, err)
	require.EqualValues(t, 2, current.FenceToken)
	require.EqualValues(t, 1, current.LastSeq)
}
