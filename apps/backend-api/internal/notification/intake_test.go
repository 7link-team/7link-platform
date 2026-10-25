package notification

import (
	"7link.com/platform/internal/notification/model"
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeFirstCardNotificationUsesCurrentSourceAndOnceOnly(t *testing.T) {
	f := newFixture(t)
	ready := true
	f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 90, true, nil
	}
	f.m.intakeTriaged = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 90, ready, nil
	}
	f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{11}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "intake", Type: "intake.triaged", Data: []byte(`{"card_revision":1}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "intake.triaged", rows[0].Type)
	ready = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
	e.SubjectID = 92
	e.Data = []byte(`{"card_revision":2}`)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestIntakeProjectionUsesConfiguredCurrentSourceAudienceAndPermanentReplay(t *testing.T) {
	f := newFixture(t)
	available := true
	f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 90, available, nil
	}
	targets := []guid.ID{11}
	f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return targets, nil }
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "intake", Type: "intake.received", Data: []byte(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "intake.received", rows[0].Type)
	eligible, err := f.m.sourceAudienceOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, eligible)
	targets = nil
	eligible, err = f.m.sourceAudienceOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, eligible, "removed target cannot read or receive a stale message")
	targets = []guid.ID{11}
	available = false
	eligible, err = f.m.sourceAudienceOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, eligible)
}

func TestIntakeChannelGroupingBoundariesReplayAndCurrentAudience(t *testing.T) {
	f := newFixture(t)
	channel := guid.ID(90)
	available := map[guid.ID]bool{91: true, 92: true, 93: true, 94: true, 95: true}
	f.m.intakeSource = func(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, channel, available[id], nil
	}
	f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{11}, nil
	}
	project := func(id guid.ID) {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			return f.m.projectEventOn(t.Context(), tx, events.RecordedEvent{ID: id + 1000, TenantID: 7, ProjectID: 81, SubjectID: id, SubjectKind: "intake", Type: "intake.received", Data: []byte(`{}`)})
		}))
	}
	rows := func() []*model.Notification {
		t.Helper()
		q := query.BindNotifications(f.db, f.m.names)
		result, err := q.WithContext(t.Context()).Order(q.ID).Find()
		require.NoError(t, err)
		return result
	}
	at := f.m.now()
	project(91)
	project(92)
	project(91)
	require.Len(t, rows(), 1)
	require.Equal(t, 2, rows()[0].MergeCount)
	available[91] = false
	row := rows()[0]
	allowed, err := f.m.intakeAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, row.MergeCount, "unavailable first source cannot hide currently readable sibling")
	q := query.BindNotifications(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(row.ID)).Update(q.ReadAt, at)
	require.NoError(t, err)
	project(92)
	require.Len(t, rows(), 1, "permanent source receipt survives marking read")
	project(93)
	require.Len(t, rows(), 2)
	f.m.now = func() time.Time { return at.Add(5 * time.Minute) }
	project(94)
	require.Len(t, rows(), 3, "exact five-minute boundary starts a new group")
	channel = 99
	project(95)
	require.Len(t, rows(), 4, "different channels never merge")
}

func TestIntakeProjectionFailuresRollbackNotificationAndReceipt(t *testing.T) {
	for _, mode := range []string{"source", "targets", "guard", "lock", "read", "receipt", "notify", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
				if mode == "source" {
					return 0, 0, false, ErrUnavailable
				}
				return 81, 90, true, nil
			}
			f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				if mode == "targets" {
					return nil, ErrUnavailable
				}
				return []guid.ID{11}, nil
			}
			switch mode {
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "missing":
				f.m.intakeSource = nil
			case "notify":
				f.ids.err = ErrUnavailable
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("intake_receipt_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.IntakeReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "receipt":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("intake_receipt_create", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.IntakeReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.projectIntakeEventOn(t.Context(), tx, events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "intake", Type: "intake.received"})
			})
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, f.db.Callback().Query().Remove("intake_receipt_read"))
			require.NoError(t, f.db.Callback().Create().Remove("intake_receipt_create"))
			for _, table := range []string{f.m.names.Notifications.String(), f.m.names.IntakeReceipts.String()} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}
