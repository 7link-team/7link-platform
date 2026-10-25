package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLeaseExpiryNoticeUsesActualOldHumanAndHourReceipt(t *testing.T) {
	f := newFixture(t)
	at := f.m.now()
	found := true
	f.m.decisionFacts.LeaseExpiredOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.LeaseExpiredNotificationFact, bool, error) {
		return delivery.LeaseExpiredNotificationFact{ProjectID: 81, WorkItemID: 91, AssigneeID: 11, EndedAt: at}, found, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "work_item.state_changed", Data: []byte(`{"reason_code":"LEASE_EXPIRED","assignment_id":"95","to":"READY"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "work_item.lease_expired", row.Type)
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			_, err := view(i18n.WithLocale(t.Context(), locale), row)
			require.NoError(t, err)
		}
	}
	found = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestProjectCreationNotifiesOnlyAddedCurrentInternalMembers(t *testing.T) {
	f := newFixture(t)
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 81, Type: "project.created", Data: []byte(`{"member_ids":["11"],"name":"private project name"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	require.NotContains(t, rows[0].Params, "private")
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	f.allow = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestPausedBudgetNoticeRequiresCurrentQualifiedReason(t *testing.T) {
	f := newFixture(t)
	state, reason := "PAUSED", "COST_CAP"
	f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
		return execution.NotificationFacts{ProjectID: 81, State: state, StateReason: reason}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "execution.state_changed", Data: []byte(`{"state":"PAUSED","reason":"COST_CAP"}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, pair := range [][2]string{{"PAUSED", "USER_REQUEST"}, {"RUNNING", "COST_CAP"}} {
		state, reason = pair[0], pair[1]
		yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, yes)
	}
	require.Equal(t, 30*time.Minute, mergeWindow("execution.cost_paused"))
}
