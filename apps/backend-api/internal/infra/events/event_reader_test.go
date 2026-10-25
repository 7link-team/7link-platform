package events

import (
	"7link.com/platform/internal/infra/events/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRecordedEventReaderKeepsCommittedIdentityAndPages(t *testing.T) {
	m, db := newTestPublisher(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, testEvent()); return err }))
	}
	rows, err := m.ReadEventsOn(t.Context(), db, 0, 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.EqualValues(t, 1, rows[0].TenantID)
	require.EqualValues(t, 2, rows[0].ProjectID)
	require.EqualValues(t, 3, rows[0].ActorID)
	require.JSONEq(t, `{"name":"Project"}`, string(rows[0].Data))
	stored, err := query.BindWithNames(db, m.names).WithContext(t.Context()).Where(query.BindWithNames(db, m.names).ID.Eq(rows[0].ID)).First()
	require.NoError(t, err)
	require.False(t, stored.OccurredAt.IsZero())
	require.True(t, stored.OccurredAt.Equal(rows[0].OccurredAt), "trigger provenance uses the persisted occurrence, not replay wall-clock")
	last, err := m.ReadEventsOn(t.Context(), db, rows[1].ID, 2)
	require.NoError(t, err)
	require.Len(t, last, 1)
	q := query.BindWithNames(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(last[0].ID)).UpdateSimple(q.ProjectID.Null(), q.ActorID.Null())
	require.NoError(t, err)
	last, err = m.ReadEventsOn(t.Context(), db, rows[1].ID, 2)
	require.NoError(t, err)
	require.Zero(t, last[0].ProjectID)
	require.Zero(t, last[0].ActorID)
	for _, limit := range []int{0, 201} {
		_, err = m.ReadEventsOn(t.Context(), db, 0, limit)
		require.ErrorIs(t, err, ErrInvalidEvent)
	}
	_, err = m.ReadEventsOn(t.Context(), nil, 0, 1)
	require.ErrorIs(t, err, ErrInvalidEvent)
	_, err = m.ReadEventsOn(t.Context(), db, -1, 1)
	require.ErrorIs(t, err, ErrInvalidEvent)
	_, err = (*Module)(nil).ReadEventsOn(t.Context(), db, 0, 1)
	require.ErrorIs(t, err, ErrInvalidEvent)
	require.NoError(t, db.Migrator().DropTable(m.names.Events.String()))
	_, err = m.ReadEventsOn(t.Context(), db, 0, 1)
	require.Error(t, err)
}
