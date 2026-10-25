package notification

import (
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/yunxiao"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestAutomationNoticePersistsRealInboxDedupeAndCurrentVisibility(t *testing.T) {
	f := newFixture(t)
	source := yunxiao.AutomationNotice{TenantID: 7, ProjectID: 81, RuleID: 90, RunID: 91, WorkItemID: 80, RecipientID: 11, RuleName: "Review", RunAs: "SYSTEM:automation:90"}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.automationNotifyOn(t.Context(), tx, source) }))
	}
	var rows []model.Notification
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "automation.notice", rows[0].Type)
	require.Equal(t, "notification.automation_notice", rows[0].TemplateKey)
	require.Contains(t, rows[0].Params, `"run_id":"91"`)
	f.allow = false
	source.RunID = 92
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.automationNotifyOn(t.Context(), tx, source) }), ErrForbidden)
	source.RunAs = "SYSTEM:automation:999"
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.automationNotifyOn(t.Context(), tx, source) }), ErrInvalid)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
