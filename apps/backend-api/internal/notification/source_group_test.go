package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacySourceGroupWithoutReceiptRechecksCurrentAudience(t *testing.T) {
	f := newFixture(t)
	current := true
	f.m.interactionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 92, 81, current, nil
	}
	row := &model.Notification{ID: 1001, TenantID: 7, ProjectID: 81, Type: "interaction.requested", Params: `{"subject_id":"91","execution_id":"92"}`}
	yes, err := f.m.sourceGroupAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.True(t, yes)
	current = false
	yes, err = f.m.sourceGroupAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.False(t, yes)
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("source_read", func(tx *gorm.DB) {
		if tx.Statement.Table == f.m.names.SourceReceipts.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	yes, err = f.m.sourceGroupAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, f.db.Callback().Query().Remove("source_read"))
}

func TestInteractionGroupSurvivingSourceExactBoundaryAndPermanentReplay(t *testing.T) {
	f := newFixture(t)
	active := map[guid.ID]bool{91: true, 93: true, 94: true}
	f.m.interactionFacts = func(_ context.Context, _ *gorm.DB, tenant, approval, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
		return 92, 81, tenant == 7 && recipient == 11 && active[approval], nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 92, Type: "execution.state_changed"}
	project := func(approval string) {
		t.Helper()
		e.Data = []byte(`{"approval_id":"` + approval + `","approval_state":"PENDING"}`)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	project("91")
	at := f.m.now()
	f.m.now = func() time.Time { return at.Add(59 * time.Second) }
	project("93")
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 2, rows[0].MergeCount)
	active[91] = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, yes)
	require.Equal(t, 1, rows[0].MergeCount)
	require.Contains(t, rows[0].Params, "93")
	f.m.now = func() time.Time { return at.Add(time.Minute) }
	project("94")
	project("93")
	rows, err = q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, 1, rows[1].MergeCount)
	active[93] = false
	yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestSourceOccurrenceReceiptFailureRollsBackInbox(t *testing.T) {
	for _, mode := range []string{"lock", "guard", "read", "receipt", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			switch mode {
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("occurrence_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.SourceReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "receipt":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("occurrence_write", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.SourceReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "notify":
				f.ids.err = ErrUnavailable
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.notifySourceOn(t.Context(), tx, sample(), 1001, 91) })
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, f.db.Callback().Query().Remove("occurrence_read"))
			require.NoError(t, f.db.Callback().Create().Remove("occurrence_write"))
			for _, table := range []string{f.m.names.Notifications.String(), f.m.names.SourceReceipts.String()} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}
