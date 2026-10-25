package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestManualReportReminderUsesCurrentStateAndSourceAudience(t *testing.T) {
	state := "DRAFT"
	eligible := true
	m := &Module{planningFacts: PlanningNotifications{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
		return PlanningNotificationFact{State: state}, true, nil
	}, EligibleOn: func(_ context.Context, _ *gorm.DB, tenant, id, actor guid.ID, kind, current string) (bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(44), id)
		require.Equal(t, guid.ID(2), actor)
		require.Equal(t, "report", kind)
		require.Equal(t, state, current)
		return eligible, nil
	}}}
	row := &model.Notification{TenantID: 1, Type: "planning.report_reminder", Params: `{"report_id":"44"}`}
	yes, err := m.sourceAudienceOn(t.Context(), nil, row, 2)
	require.NoError(t, err)
	require.True(t, yes)
	eligible = false
	yes, err = m.sourceAudienceOn(t.Context(), nil, row, 2)
	require.NoError(t, err)
	require.False(t, yes)
	eligible = true
	for _, s := range []string{"SUBMITTED", "CONFIRMED"} {
		state = s
		yes, err = m.sourceAudienceOn(t.Context(), nil, row, 2)
		require.NoError(t, err)
		require.False(t, yes)
	}
	row.Params = `{"report_id":"invalid"}`
	_, err = m.sourceAudienceOn(t.Context(), nil, row, 2)
	require.ErrorIs(t, err, ErrInvalid)
}
