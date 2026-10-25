package notification

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAndonNoticeUsesCurrentPullEpisodeAndSuppressesClear(t *testing.T) {
	f := newFixture(t)
	active, eligible := true, true
	pull := guid.ID(900)
	f.m.decisionFacts.AndonOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (delivery.AndonNotificationFact, bool, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, id)
		return delivery.AndonNotificationFact{ProjectID: 9, WorkItemID: 91, AccountableID: 12, OccurrenceID: pull}, active, nil
	}
	f.m.decisionFacts.AndonEligibleOn = func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
		return eligible && actor == 12, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "work_item", Type: "work_item.state_changed", Data: []byte(`{"reason_code":"ANDON_PULLED"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("andon.pulled")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, "URGENT", rows[0].Severity)
	require.Equal(t, 1, rows[0].MergeCount)
	for _, condition := range []string{"cleared", "revoked", "new_pull"} {
		active, eligible, pull = true, true, 900
		switch condition {
		case "cleared":
			active = false
		case "revoked":
			eligible = false
		case "new_pull":
			pull = 901
		}
		yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
		require.NoError(t, err)
		require.False(t, yes, condition)
	}
	active, eligible, pull = true, true, 900
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.True(t, yes)
}

func TestAndonProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.decisionFacts.AndonOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.AndonNotificationFact, bool, error) {
				if mode == "source" {
					return delivery.AndonNotificationFact{}, false, ErrUnavailable
				}
				return delivery.AndonNotificationFact{ProjectID: 81, WorkItemID: 91, AccountableID: 11, OccurrenceID: 900}, true, nil
			}
			f.m.decisionFacts.AndonEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "work_item", Type: "andon.pulled", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.decisionFacts.AndonOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAndonOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
