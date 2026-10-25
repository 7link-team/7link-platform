package events

import (
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestParkedNotificationJoinsFailureTransactionAndIgnoresDuplicates(t *testing.T) {
	m, db := newTestSequencer(t)
	require.NoError(t, db.Exec("CREATE TABLE parked_notification_probe (id BIGINT PRIMARY KEY)").Error)
	s := module.NewServices()
	require.NoError(t, m.provideParkedNotifications(s))
	require.NoError(t, s.Seal())
	hook, err := module.Resolve[ParkedNotifications](s)
	require.NoError(t, err)
	m.sealed = false
	require.ErrorIs(t, hook.Bind(nil), ErrInvalidEvent)
	require.NoError(t, hook.Bind(func(ctx context.Context, tx *gorm.DB, n ParkedNotice) error {
		require.Equal(t, "test.parked", n.Subscriber)
		require.EqualValues(t, 1, n.Count)
		return tx.WithContext(ctx).Exec("INSERT INTO parked_notification_probe VALUES (?)", n.EventID).Error
	}))
	require.Error(t, hook.Bind(func(context.Context, *gorm.DB, ParkedNotice) error { return nil }))
	m.sealed = true
	emitEvents(t, m, db, 1)
	q := query.BindWithNames(db, m.names)
	row, err := q.WithContext(t.Context()).Take()
	require.NoError(t, err)
	rollback := errors.New("abort")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		if x := m.park(t.Context(), tx, "test.parked", 0, envelope(row), context.Canceled); x != nil {
			return x
		}
		return rollback
	}), rollback)
	var count int64
	require.NoError(t, db.Table("parked_notification_probe").Count(&count).Error)
	require.Zero(t, count)
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.park(t.Context(), tx, "test.parked", 0, envelope(row), context.Canceled)
		}))
	}
	require.NoError(t, db.Table("parked_notification_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestParkedNotificationStorageAndDeliveryFailuresAbortTransaction(t *testing.T) {
	m, db := newTestSequencer(t)
	emitEvents(t, m, db, 1)
	events := query.BindWithNames(db, m.names)
	event, err := events.WithContext(t.Context()).Take()
	require.NoError(t, err)
	m.onParked = func(context.Context, *gorm.DB, ParkedNotice) error { return errors.New("notification rejected") }
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
		return m.park(t.Context(), tx, "test.parked", 0, envelope(event), context.Canceled)
	}), "notification rejected")
	p := query.BindParked(db, m.names)
	count, err := p.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	m.onParked = func(context.Context, *gorm.DB, ParkedNotice) error { return nil }
	for _, failAt := range []int{1, 2} {
		calls := 0
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("park_count_failed", func(tx *gorm.DB) {
			if tx.Statement.Table == m.names.Parked.String() {
				calls++
				if calls == failAt {
					tx.AddError(errors.New("park count unavailable"))
				}
			}
		}))
		require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
			return m.park(t.Context(), tx, "test.parked", 0, envelope(event), context.Canceled)
		}), "park count unavailable")
		require.NoError(t, db.Callback().Query().Remove("park_count_failed"))
	}
	m.ids = &testIDs{err: errors.New("ids unavailable")}
	require.ErrorContains(t, m.recordReceipt(t.Context(), db, "test.parked", envelope(event)), "ids unavailable")
	require.ErrorContains(t, m.park(t.Context(), db, "test.parked", 0, envelope(event), context.Canceled), "ids unavailable")
}
