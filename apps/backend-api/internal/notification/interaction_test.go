package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestInteractionProjectionUsesCurrentEligibilityAndPermanentReplay(t *testing.T) {
	f := newFixture(t)
	eligible := true
	f.m.interactionFacts = func(_ context.Context, _ *gorm.DB, tenant, approval, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), approval)
		return 92, 81, eligible && recipient == 11, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 92, Type: "execution.state_changed", Data: json.RawMessage(`{"approval_id":"91","approval_state":"PENDING","input":"private input"}`)}
	project := func() {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	project()
	at := f.m.now()
	f.m.now = func() time.Time { return at.Add(2 * time.Hour) }
	project()
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "interaction.requested", rows[0].Type)
	require.Equal(t, 1, rows[0].MergeCount)
	require.NotContains(t, rows[0].Params, "private")
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, yes)
	eligible = false
	yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestInteractionProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.interactionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
				if mode == "source" {
					return 0, 0, false, ErrUnavailable
				}
				return 91, 81, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91}
			switch mode {
			case "missing":
				f.m.interactionFacts = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.projectInteractionOn(t.Context(), tx, e, map[string]json.RawMessage{"approval_state": []byte(`"PENDING"`), "approval_id": []byte(`"90"`)})
			}))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
