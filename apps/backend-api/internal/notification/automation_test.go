package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/internal/yunxiao"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutomationFailuresGroupByRuleWithPermanentRunReceiptsAndCurrentAudience(t *testing.T) {
	f := newFixture(t)
	eligible := map[guid.ID]bool{11: true, 12: true}
	f.m.automationFacts = yunxiao.AutomationNotifications{
		RunOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (yunxiao.AutomationFailureFact, bool, error) {
			return yunxiao.AutomationFailureFact{ID: id, ProjectID: 81, RuleID: 90, CreatorID: 11, WorkItemID: 80, RuleName: "Check workflow", Error: "ACTION_FAILED"}, tenant == 7 && id >= 91, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) { return eligible[actor], nil },
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "automation_run", Type: "automation.run_failed"}
	consume := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAutomationFailureOn(t.Context(), tx, e) }))
	}
	consume()
	consume()
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, 1, row.MergeCount)
	}
	e.ID++
	e.SubjectID = 92
	consume()
	consume()
	rows, err = q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, 2, row.MergeCount)
		yes, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.True(t, yes)
	}
	// Revoking a source audience removes the grouped content, even while generic
	// project permission remains unchanged.
	eligible[11] = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
	eligible[11] = true
	// Reading the first group permits a new group but never a replayed run.
	at := f.m.now()
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(rows[0].ID)).Update(q.ReadAt, &at)
	require.NoError(t, err)
	consume()
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	e.ID++
	e.SubjectID = 93
	consume()
	count, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
	f.m.now = func() time.Time { return at.Add(time.Hour) }
	e.ID++
	e.SubjectID = 94
	consume()
	count, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 5, count)
	receipts := query.BindSourceReceipts(f.db, f.m.names)
	n, err := receipts.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 8, n)
}

func TestAutomationProjectionRejectsMissingForeignAndUnavailableSources(t *testing.T) {
	f := newFixture(t)
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "automation_run", Type: "automation.run_failed"}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAutomationFailureOn(t.Context(), tx, e) }), ErrUnavailable)
	found := false
	f.m.automationFacts = yunxiao.AutomationNotifications{
		RunOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (yunxiao.AutomationFailureFact, bool, error) {
			return yunxiao.AutomationFailureFact{ID: 91, ProjectID: 82, RuleID: 90, CreatorID: 11, WorkItemID: 80}, found, nil
		},
		EligibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil },
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAutomationFailureOn(t.Context(), tx, e) }))
	found = true
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAutomationFailureOn(t.Context(), tx, e) }), ErrInvalid)
	f.m.automationFacts.RunOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (yunxiao.AutomationFailureFact, bool, error) {
		return yunxiao.AutomationFailureFact{}, false, errors.New("source unavailable")
	}
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAutomationFailureOn(t.Context(), tx, e) }))
	for _, params := range []string{"bad", `{}`, `{"subject_id":"0"}`} {
		_, err := f.m.automationAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Type: e.Type, Params: params}, 11)
		require.Error(t, err)
	}
	q := query.BindNotifications(f.db, f.m.names)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestAutomationProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.automationFacts.RunOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (yunxiao.AutomationFailureFact, bool, error) {
				if mode == "source" {
					return yunxiao.AutomationFailureFact{}, false, ErrUnavailable
				}
				return yunxiao.AutomationFailureFact{ID: 91, ProjectID: 81, RuleID: 90, CreatorID: 11, WorkItemID: 80}, true, nil
			}
			f.m.automationFacts.EligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "automation_run", Type: "automation.run_failed", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.automationFacts.RunOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
