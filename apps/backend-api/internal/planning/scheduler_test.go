package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"text/template"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	pi "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type planningTestNotifier struct{ fail bool }

type failingPlanningPublisher struct{}

func (failingPlanningPublisher) Publish(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
	return 0, errors.New("outbox failed")
}

func (n planningTestNotifier) Notify(ctx context.Context, tx *gorm.DB, in notification.Notification) ([]guid.ID, error) {
	if err := tx.WithContext(ctx).Exec("INSERT INTO planning_notice_probe VALUES (?,?)", in.Recipients[0], in.Params["report_id"]).Error; err != nil {
		return nil, err
	}
	if n.fail {
		return nil, errors.New("notify unavailable")
	}
	return []guid.ID{1}, nil
}
func (planningTestNotifier) SendEmail(context.Context, *gorm.DB, notification.EmailMessage) (guid.ID, error) {
	return 0, errors.New("not used")
}

func TestReportOverdueSchedulePreservesDueSnapshotAndCapsRealNotifications(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).First(&row).Error)
	require.NotNil(t, row.DueAt)
	_, end, e := reportPeriod(&row)
	require.NoError(t, e)
	require.Equal(t, end.Add(1620*time.Minute), *row.DueAt)
	original := *row.DueAt
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{GraceWeekMinutes: settingPtr(0), Timezone: settingPtr("UTC")})
		return e
	}))
	now := original
	h.m.now = func() time.Time { return now }
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 0, noticeCount(t, h))
	now = now.Add(time.Second)
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 1, noticeCount(t, h))
	for i := 0; i < 3; i++ {
		require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	}
	require.EqualValues(t, 1, noticeCount(t, h))
	now = original.Add(24 * time.Hour)
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 2, noticeCount(t, h))
	now = original.Add(48 * time.Hour)
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 2, noticeCount(t, h))
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).First(&row).Error)
	require.Equal(t, "OVERDUE", row.State)
	require.Equal(t, 2, row.OverdueNoticeCount)
	require.Equal(t, original, *row.DueAt)
	s := h.m.JobSchedules()
	require.Len(t, s, 5)
	require.Equal(t, 10*time.Minute, s[0].Interval)
	require.Equal(t, jobs.ScopePerTenant, s[0].TenantScope)
}

func noticeCount(t *testing.T, h harness) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	return count
}

func TestOverdueNotificationFailureRollsBackReportAndNotice(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).First(&row).Error)
	now := row.DueAt.Add(time.Second)
	h.m.now = func() time.Time { return now }
	h.m.reportNotifier = planningTestNotifier{fail: true}
	require.Error(t, h.m.scanOverdue(t.Context(), 1))
	require.Zero(t, noticeCount(t, h))
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).First(&row).Error)
	require.Equal(t, "DRAFT", row.State)
	require.Equal(t, 1, row.Version)
	require.Zero(t, row.OverdueNoticeCount)
	h.m.reportNotifier = planningTestNotifier{}
	originalAudit := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	require.Error(t, h.m.scanOverdue(t.Context(), 1))
	require.Zero(t, noticeCount(t, h))
	h.m.audit = originalAudit
	originalPublisher := h.m.events
	h.m.events = failingPlanningPublisher{}
	require.Error(t, h.m.scanOverdue(t.Context(), 1))
	require.Zero(t, noticeCount(t, h), "notification and report both roll back if outbox fails")
	h.m.events = originalPublisher
	require.NoError(t, h.m.scanOverdue(t.Context(), 2))
	require.Zero(t, noticeCount(t, h))
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 1, noticeCount(t, h))
}

func TestOverdueRecipientUsesCurrentAgentOwnerAndHandlesUnavailableIdentity(t *testing.T) {
	h := setup(t)
	for _, tc := range []struct{ subject, author, want guid.ID }{{2, 2, 2}, {7, 7, 2}, {0, 3, 3}, {0, 0, 0}} {
		recipient, e := h.m.reportRecipientOn(t.Context(), h.db, &model.Report{TenantID: 1, PrincipalID: tc.subject, CreatedBy: tc.author})
		require.NoError(t, e)
		require.Equal(t, tc.want, recipient)
	}
	r := &model.Report{TenantID: 1, PrincipalID: 7}
	h.m.agents.ByPrincipalOn = nil
	principal, e := h.m.reportRecipientOn(t.Context(), h.db, r)
	require.NoError(t, e)
	require.Zero(t, principal)
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, errors.New("directory unavailable")
	}
	_, e = h.m.reportRecipientOn(t.Context(), h.db, r)
	require.Error(t, e)
	h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, errors.New("identity unavailable")
	}
	_, e = h.m.reportRecipientOn(t.Context(), h.db, r)
	require.Error(t, e)
}

func TestOverdueJobHandlerAndInvalidScopeFailClosed(t *testing.T) {
	h := setup(t)
	handlers := h.m.JobHandlers()
	require.Len(t, handlers, 5)
	require.Equal(t, "planning.report_overdue", handlers[0].Kind)
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{}), ErrInvalid)
	require.NoError(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 2}))
	h.m.reportNotifier = nil
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
	require.ErrorIs(t, h.m.initializeReportDueOn(t.Context(), h.db, 1, nil), ErrInvalid)
	require.ErrorIs(t, h.m.initializeReportDueOn(t.Context(), h.db, 1, &model.Report{TenantID: 2}), ErrInvalid)
	require.Error(t, h.m.initializeReportDueOn(t.Context(), h.db, 1, &model.Report{TenantID: 1, Content: "invalid"}))
}

func TestOverdueRecipientRequiresCurrentActiveMatchingIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		agent       fleet.AgentIdentity
		ownerActive bool
		ownerID     guid.ID
	}{
		{"inactive agent", fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, OwnerPrincipalID: 2, State: "SUSPENDED"}, true, 2},
		{"wrong tenant", fleet.AgentIdentity{TenantID: 2, PrincipalID: 7, OwnerPrincipalID: 2, State: "ACTIVE"}, true, 2},
		{"wrong agent", fleet.AgentIdentity{TenantID: 1, PrincipalID: 70, OwnerPrincipalID: 2, State: "ACTIVE"}, true, 2},
		{"inactive owner", fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, OwnerPrincipalID: 2, State: "ACTIVE"}, false, 2},
		{"wrong owner", fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, OwnerPrincipalID: 2, State: "ACTIVE"}, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return tc.agent, true, nil
			}
			h.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: tc.ownerID}, id != 7 && tc.ownerActive, nil
			}
			recipient, err := h.m.reportRecipientOn(t.Context(), h.db, &model.Report{TenantID: 1, PrincipalID: 7})
			require.NoError(t, err)
			require.Zero(t, recipient)
		})
	}
}

func TestOverdueDeliveryRechecksPrincipalLockAndAgentOwner(t *testing.T) {
	for _, mode := range []string{"inactive lock", "wrong locked identity", "owner changed"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			draft := reportDraft(t, h)
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", draft.ID).Update("principal_id", 7).Error)
			h.m.now = func() time.Time { return draft.DueAt.Add(time.Second) }
			owner := guid.ID(2)
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, OwnerPrincipalID: owner, State: "ACTIVE"}, true, nil
			}
			h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if mode == "owner changed" {
					owner = 3
				}
				if mode == "wrong locked identity" {
					return identity.Principal{ID: 3}, true, nil
				}
				return identity.Principal{ID: id}, mode != "inactive lock", nil
			}
			require.NoError(t, h.m.scanOverdue(t.Context(), 1))
			require.Zero(t, noticeCount(t, h), "no stale or inactive recipient may be notified")
			if mode == "owner changed" {
				require.NoError(t, h.m.scanOverdue(t.Context(), 1))
				require.EqualValues(t, 1, noticeCount(t, h))
				var recipient guid.ID
				require.NoError(t, h.db.Table("planning_notice_probe").Select("recipient").Scan(&recipient).Error)
				require.Equal(t, guid.ID(3), recipient)
			}
		})
	}
}

func TestOverdueTemplatesRenderRealParametersInEveryLocale(t *testing.T) {
	h := setup(t)
	require.NoError(t, h.m.Initialize(t.Context()))
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, suffix := range []string{"_title", "_body", "_email_subject", "_email_text", "_email_html"} {
			key := "planning.report_overdue" + suffix
			text := pi.TContext(pi.WithLocale(t.Context(), locale), key)
			require.NotEqual(t, key, text)
			tpl, e := template.New(key).Option("missingkey=error").Parse(text)
			require.NoError(t, e)
			var b bytes.Buffer
			require.NoError(t, tpl.Execute(&b, map[string]string{"report_id": "9001", "period_kind": "WEEK", "period_start": "2026-10-19"}))
			require.NotContains(t, b.String(), "{{")
			if suffix == "_body" || suffix == "_email_text" || suffix == "_email_html" {
				require.Contains(t, b.String(), "9001")
				require.Contains(t, b.String(), "2026-10-19")
			}
		}
	}
}

func TestOverdueReportRemainsSubmittableAndLeavesAuthorInbox(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	draft := reportDraft(t, h)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(draft.Content, &body))
	body.Uncompleted = "Unfinished work"
	body.Retrospective = "Review actual evidence"
	body.NextPlan = []PlanningPlanItem{linkedItem(goal)}
	body.DeclaredConfidence = "YELLOW"
	var edited PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		edited, e = h.m.editReportOn(claims(t, 2), tx, 1, draft.ID, 1, body)
		return e
	}))
	now := draft.DueAt.Add(time.Second)
	h.m.now = func() time.Time { return now }
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "REPORT_OVERDUE", Limit: 10}
	items, e := h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, draft.ID, items[0].ID)
	req.PrincipalID = 3
	items, e = h.m.reportOverdueInbox(claims(t, 3), req)
	require.NoError(t, e)
	require.Empty(t, items)
	var submitted PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		submitted, e = h.m.decideReportOn(claims(t, 2), tx, 1, draft.ID, PlanningDecisionInput{Version: edited.Version + 1, Action: "SUBMIT", ExpectedContentHash: edited.ContentHash})
		return e
	}))
	require.Equal(t, "SUBMITTED", submitted.State)
	require.True(t, submitted.SubmittedLate)
	req.PrincipalID = 2
	items, e = h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, e)
	require.Empty(t, items)
	now = now.Add(48 * time.Hour)
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.EqualValues(t, 1, noticeCount(t, h))
}

// Capture the actual notifier command rather than assuming the first audience
// entry represents the complete overdue audience.
type reportAudienceNotifier struct {
	planningTestNotifier
	recipients []guid.ID
}

func (n *reportAudienceNotifier) Notify(_ context.Context, _ *gorm.DB, in notification.Notification) ([]guid.ID, error) {
	n.recipients = append(n.recipients, in.Recipients...)
	return []guid.ID{1}, nil
}
func TestOverdueIncludesCurrentSuperiorAndAuthorWithoutDuplicateAudience(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
	h.m.now = func() time.Time { return row.DueAt.Add(time.Second) }
	notifier := &reportAudienceNotifier{}
	h.m.reportNotifier = notifier
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.ElementsMatch(t, []guid.ID{2, 3}, notifier.recipients)
	require.NoError(t, h.m.scanOverdue(t.Context(), 1))
	require.Len(t, notifier.recipients, 2, "scan replay preserves occurrence cap")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for _, tc := range []struct {
			id   guid.ID
			want bool
		}{{2, true}, {3, true}, {4, false}, {7, false}} {
			yes, err := h.m.planningNotificationEligibleOn(t.Context(), tx, 1, r.ID, tc.id, "report", "OVERDUE_AUDIENCE")
			require.NoError(t, err)
			require.Equal(t, tc.want, yes)
		}
		h.m.reportSuperior = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 4, nil }
		yes, err := h.m.planningNotificationEligibleOn(t.Context(), tx, 1, r.ID, 3, "report", "OVERDUE_AUDIENCE")
		require.NoError(t, err)
		require.False(t, yes, "old superior loses visibility immediately")
		return nil
	}))
}

func TestOverdueRecheckSourceFailuresPreserveReportAndNotice(t *testing.T) {
	for _, mode := range []string{"recipient", "principal_lock", "report_read", "confirmer", "superior_lock", "write"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			r := reportDraft(t, h)
			var row model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
			now := row.DueAt.Add(time.Second)
			h.m.now = func() time.Time { return now }
			switch mode {
			case "recipient":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "principal_lock", "superior_lock":
				original := h.m.principals.LockActiveRow
				h.m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if mode == "principal_lock" || id == 3 {
						return identity.Principal{}, false, ErrUnavailable
					}
					return original(ctx, tx, id)
				}
			case "report_read":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("overdue_report_read", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "confirmer":
				h.m.reportSuperior = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 0, ErrUnavailable }
			case "write":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("overdue_report_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.overdueReportOn(t.Context(), tx, &row) })
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, h.db.Callback().Query().Remove("overdue_report_read"))
			require.NoError(t, h.db.Callback().Update().Remove("overdue_report_write"))
			var current model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&current).Error)
			require.Equal(t, row.Version, current.Version)
			require.Equal(t, "DRAFT", current.State)
			require.Zero(t, noticeCount(t, h))
		})
	}
}
