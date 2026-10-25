package planning

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

type reminderNotifier struct{ notices []notification.Notification }

func (n *reminderNotifier) Notify(_ context.Context, _ *gorm.DB, in notification.Notification) ([]guid.ID, error) {
	n.notices = append(n.notices, in)
	return []guid.ID{1}, nil
}
func (*reminderNotifier) SendEmail(context.Context, *gorm.DB, notification.EmailMessage) (guid.ID, error) {
	return 0, ErrUnavailable
}

func TestPlanningReminderCurrentAuthorityDeadlineAndClosure(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	plan := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	at := h.m.now()
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", plan.ID).Updates(map[string]any{"state": "SUBMITTED", "updated_at": at, "submitted_at": at}).Error)
	sink := &reminderNotifier{}
	h.m.reportNotifier = sink
	h.m.notificationCandidates = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]guid.ID, error) {
		if after > 0 {
			return nil, nil
		}
		return []guid.ID{2, 3, 7}, nil
	}
	run := func() {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.notificationReminderOn(t.Context(), tx, 1, plan.ID, "plan") }))
	}
	h.m.now = func() time.Time { return at.Add(72*time.Hour - time.Millisecond) }
	run()
	require.Empty(t, sink.notices)
	h.m.now = func() time.Time { return at.Add(72 * time.Hour) }
	run()
	require.Len(t, sink.notices, 1)
	require.Equal(t, []guid.ID{3}, sink.notices[0].Recipients)
	require.Equal(t, "planning.plan_unconfirmed", sink.notices[0].Type)
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", plan.ID).Update("state", "CONFIRMED").Error)
	run()
	require.Len(t, sink.notices, 1)
	// A real active member goal is due seven calendar days after its source period.
	var goal model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("principal_id=?", 2).First(&goal).Error)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("state", "ACTIVE").Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		f, _, err := h.m.planningNotificationFactOn(t.Context(), tx, 1, goal.ID, "goal")
		require.NoError(t, err)
		require.NotNil(t, f.ReminderDueAt)
		due := *f.ReminderDueAt
		h.m.now = func() time.Time { return due }
		return h.m.notificationReminderOn(t.Context(), tx, 1, goal.ID, "goal")
	}))
	require.Len(t, sink.notices, 2)
	require.Equal(t, []guid.ID{2}, sink.notices[1].Recipients)
	require.Equal(t, "planning.goal_close_overdue", sink.notices[1].Type)
}

func TestPlanTimeoutUsesSubmissionClockAndPreservesConfirmationAndReportBaseline(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	at := h.m.now()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "SUBMIT"})
		return err
	}))
	var row model.Plan
	read := func() {
		row = model.Plan{}
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).First(&row).Error)
	}
	read()
	require.NotNil(t, row.SubmittedAt)
	require.True(t, at.Equal(*row.SubmittedAt))
	h.m.now = func() time.Time { return at.Add(71 * time.Hour) }
	var body PlanningPlanInput
	require.NoError(t, json.Unmarshal(p.Content, &body))
	body.Items[0].Title = "Updated pending plan"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
		return err
	}))
	read()
	require.True(t, at.Equal(*row.SubmittedAt), "editing cannot postpone confirmation deadline")
	require.NoError(t, h.db.Exec("CREATE TABLE plan_notice_probe (recipient BIGINT, dedupe TEXT, UNIQUE(recipient,dedupe))").Error)
	sink := &transactionalPlanReminder{}
	h.m.reportNotifier = sink
	h.m.notificationCandidates = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]guid.ID, error) {
		if after > 0 {
			return nil, nil
		}
		return []guid.ID{2, 3, 7}, nil
	}
	run := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.notificationReminderOn(t.Context(), tx, 1, p.ID, "plan") })
	}
	h.m.now = func() time.Time { return at.Add(72*time.Hour - time.Millisecond) }
	require.NoError(t, run())
	read()
	require.Nil(t, row.UnconfirmedAt)
	require.Empty(t, sink.notices)
	h.m.now = func() time.Time { return at.Add(72 * time.Hour) }
	originalAudit := h.m.audit
	failure := errors.New("audit unavailable")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, run(), failure)
	read()
	require.Nil(t, row.UnconfirmedAt)
	require.Equal(t, p.Version, row.Version)
	h.m.audit = originalAudit
	h.m.events = failingPlanningPublisher{}
	require.Error(t, run())
	read()
	require.Nil(t, row.UnconfirmedAt)
	// Restore the actual outbox publisher from the harness services.
	publisher, err := module.Resolve[events.Publisher](h.s)
	require.NoError(t, err)
	h.m.events = publisher
	require.NoError(t, run())
	read()
	require.NotNil(t, row.UnconfirmedAt)
	require.Equal(t, "SUBMITTED", row.State)
	version := row.Version
	var auditCount int64
	require.NoError(t, h.db.Table("audit_probe").Count(&auditCount).Error)
	require.NoError(t, run())
	read()
	require.Equal(t, version, row.Version)
	var noticeCount int64
	require.NoError(t, h.db.Table("plan_notice_probe").Count(&noticeCount).Error)
	require.EqualValues(t, 1, noticeCount, "failed transactions and repeat scans cannot duplicate a notice")
	var afterCount int64
	require.NoError(t, h.db.Table("audit_probe").Count(&afterCount).Error)
	require.Equal(t, auditCount, afterCount, "repeat timeout emits no duplicate audit")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.readOn(claims(t, 3), tx, 1, p.ID, "plan")
		return err
	}))
	require.True(t, p.Unconfirmed)
	report := reportDraft(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		r, _, err := h.m.reportOn(claims(t, 2), tx, 1, report.ID, "report_read", false)
		if err != nil {
			return err
		}
		check, err := h.m.checkReport(claims(t, 2), tx, 1, r)
		require.NoError(t, err)
		require.Contains(t, check.Issues, PlanningIssue{Code: "N2_PLAN_UNCONFIRMED", Blocking: false})
		return err
	}))

	items, err := h.m.planningInbox(claims(t, 3), governance.InboxRequest{TenantID: 1, PrincipalID: 3, Kind: "PLAN_CONFIRMATION", Limit: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, []guid.ID{3}, sink.notices[len(sink.notices)-1].Recipients)
	for _, actor := range []guid.ID{2, 7} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.m.decidePlanOn(claims(t, actor), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "CONFIRM"})
			return err
		}), ErrForbidden)
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.decidePlanOn(claims(t, 3), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "CONFIRM"})
		return err
	}))
	require.False(t, p.Unconfirmed)
	h.m.now = func() time.Time { return at.Add(80 * time.Hour) }
	body.Items[0].Title = "New confirmed scope"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
		return err
	}))
	read()
	require.Equal(t, "SUBMITTED", row.State)
	require.Nil(t, row.UnconfirmedAt)
	require.True(t, h.m.now().Equal(*row.SubmittedAt))
	require.NoError(t, run())
	read()
	require.Nil(t, row.UnconfirmedAt)
}

type transactionalPlanReminder struct{ reminderNotifier }

func (n *transactionalPlanReminder) Notify(ctx context.Context, tx *gorm.DB, in notification.Notification) ([]guid.ID, error) {
	result := tx.WithContext(ctx).Exec("INSERT INTO plan_notice_probe (recipient,dedupe) VALUES (?,?) ON CONFLICT DO NOTHING", in.Recipients[0], in.DedupeKey)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return n.reminderNotifier.Notify(ctx, tx, in)
}

func TestReminderScannerChecksSourcesAndRollsBackUnconfirmedMarker(t *testing.T) {
	for _, mode := range []string{"success", "plan_read", "goal_read", "candidates", "ordering", "missing_notifier", "write"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
			submitted := h.m.now().Add(-73 * time.Hour)
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Updates(map[string]any{"state": "SUBMITTED", "submitted_at": submitted}).Error)
			sink := &reminderNotifier{}
			h.m.reportNotifier = sink
			h.m.notificationCandidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "ordering" {
					return []guid.ID{3, 2}, nil
				}
				return []guid.ID{3}, nil
			}
			if mode == "missing_notifier" {
				h.m.reportNotifier = nil
			}
			if mode == "plan_read" || mode == "goal_read" {
				table := h.m.names.Plans.String()
				if mode == "goal_read" {
					table = h.m.names.Goals.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("reminder_scan_read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if mode == "write" {
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("reminder_scan_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Plans.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.m.scanNotificationReminders(t.Context(), 1)
			require.NoError(t, h.db.Callback().Query().Remove("reminder_scan_read"))
			require.NoError(t, h.db.Callback().Update().Remove("reminder_scan_write"))
			if mode == "success" {
				require.NoError(t, err)
				require.Len(t, sink.notices, 1)
			} else {
				require.Error(t, err)
			}
			var row model.Plan
			require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).First(&row).Error)
			require.Equal(t, mode == "success" || mode == "goal_read", row.UnconfirmedAt != nil, "scan commits each source in its own transaction")
		})
	}
	h := setup(t)
	require.ErrorIs(t, h.m.scanNotificationReminders(t.Context(), 0), ErrInvalid)
	require.ErrorIs(t, h.m.scanNotificationReminders(claims(t, 2), 1), ErrForbidden)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.notificationReminderOn(claims(t, 2), tx, 1, 1, "plan") }), ErrForbidden)
	h.m.runtime = nil
	require.ErrorIs(t, h.m.scanNotificationReminders(t.Context(), 1), ErrUnavailable)
}
