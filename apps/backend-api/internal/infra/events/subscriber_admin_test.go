package events

import (
	"7link.com/platform/internal/infra/events/query"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestReplayUsesIndependentDurableCursorAndReceiptPolicy(t *testing.T) {
	m, db := newTestSequencer(t)
	var seen []int64
	registerTestSubscriber(t, m, Subscriber{Name: "test.replay", StartFrom: Earliest, EventTypes: []string{"project.*"}, Handle: func(_ context.Context, _ *gorm.DB, e Envelope) error { seen = append(seen, e.StreamSeq); return nil }})
	emitEvents(t, m, db, 5)
	_, err := m.DispatchPartition(t.Context(), "test.replay", 0)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, seen)
	for _, ignore := range []bool{false, true} {
		seen = nil
		var status ReplayStatus
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var x error
			status, x = m.ReplayOn(t.Context(), tx, "test.replay", 2, 4, ignore)
			return x
		}))
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.ReplayOn(t.Context(), tx, "test.replay", 2, 4, ignore); return x }), ErrReplayActive)
		_, err = m.DispatchPartition(t.Context(), status.ReplayCursor, 0)
		require.NoError(t, err)
		if ignore {
			require.Equal(t, []int64{2, 3, 4}, seen)
		} else {
			require.Empty(t, seen)
		}
		_, err = m.DispatchPartition(t.Context(), status.ReplayCursor, 0)
		require.NoError(t, err)
		q := query.BindCursors(db, m.names)
		count, x := q.WithContext(t.Context()).Where(q.Consumer.Eq(status.ReplayCursor)).Count()
		require.NoError(t, x)
		require.Zero(t, count)
		cursor, x := q.WithContext(t.Context()).Where(q.Consumer.Eq("test.replay")).Take()
		require.NoError(t, x)
		require.EqualValues(t, 5, cursor.LastSeq)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, x := m.ReplayOn(t.Context(), tx, "test.replay", 0, 5, true); return x }), ErrReplayRange)
	require.ErrorIs(t, m.SetPaused(t.Context(), "absent", true), ErrSubscriberNotFound)
	statuses, err := m.Subscribers(t.Context())
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.EqualValues(t, 0, statuses[0].Cursors[0].Lag)
}
