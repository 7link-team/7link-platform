package notification

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHumanVerificationNoticeRechecksPendingIndependentInstruction(t *testing.T) {
	f := newFixture(t)
	pending := true
	f.m.decisionFacts.HumanVerificationOn = func(_ context.Context, _ *gorm.DB, tenant, work, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
		return 99, 81, pending && tenant == 7 && work == 91 && recipient == 12, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "work_item.escalated", Data: []byte(`{"reason_code":"MANDATORY_HUMAN_GATE","detail":"private plan"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "verification.human_requested", rows[0].Type)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.NotContains(t, rows[0].Params, "private")
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	pending = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, yes)
}
