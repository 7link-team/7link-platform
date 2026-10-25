package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func automationReportHarness(t *testing.T) (harness, PlanningRecord, context.Context, yunxiao.AutomationReportEntry) {
	h := setup(t)
	r := reportDraft(t, h)
	h.m.scheduledFacts = delivery.ScheduledPlanningFacts{ReadOn: func(_ context.Context, tx *gorm.DB, tenant, project, subject guid.ID, _, _ time.Time) (delivery.PlanningSnapshot, error) {
		require.Contains(t, tx.Statement.Clauses, "FOR", "SYSTEM action facts must escape any earlier consistent snapshot")
		if tenant != 1 || project != 3 || subject != 2 {
			return delivery.PlanningSnapshot{}, ErrForbidden
		}
		return *h.facts, nil
	}}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, EntityID: 3, SubjectType: authn.SubjectTypePrincipal})
	in := yunxiao.AutomationReportEntry{TenantID: 1, ProjectID: 3, CreatorID: 3, PrincipalID: 2, WorkItemID: 101, RuleID: 60, RunID: 61, RunAs: "SYSTEM:automation:60", Section: "uncompleted", OccurredAt: h.m.now()}
	return h, r, ctx, in
}

func TestAutomationReportWritesActualDraftFactsAsSystemAndReplays(t *testing.T) {
	h, r, ctx, in := automationReportHarness(t)
	for range 2 {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }))
	}
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
	require.Equal(t, r.Version+1, row.Version)
	require.Equal(t, guid.ID(2), row.CreatedBy)
	require.Equal(t, "DRAFT", row.State)
	require.True(t, row.NeedsRecheck)
	require.Empty(t, row.LastCheckHash)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal([]byte(row.Content), &body))
	require.Len(t, body.UncompletedItems, 1)
	require.Equal(t, in.WorkItemID, *body.UncompletedItems[0].Item.WorkItemID)
	require.Equal(t, "IN_PROGRESS", body.UncompletedItems[0].Detail)
	require.Equal(t, "CARRY_TO_NEXT", body.UncompletedItems[0].CarryOver)
	// A current acceptance can populate completed, but duplicates never inflate it.
	in.Section = "completed"
	in.WorkItemID = 100
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }))
	var again model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&again).Error)
	require.Equal(t, row.Version, again.Version)
	// Refuse promoting an unaccepted work item to completed.
	in.WorkItemID = 101
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }), ErrConflict)
}

func TestAutomationReportRechecksAuthorityAndRollsBackEveryWrite(t *testing.T) {
	for _, failure := range []string{"human context", "tenant", "permission", "member", "source", "audit", "outbox", "confirmed", "missing draft"} {
		t.Run(failure, func(t *testing.T) {
			h, r, ctx, in := automationReportHarness(t)
			problem := errors.New("report action dependency failed")
			switch failure {
			case "human context":
				ctx = claims(t, 3)
			case "tenant":
				in.TenantID = 2
			case "permission":
				h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "member":
				in.CreatorID = 9
			case "source":
				h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, problem
				}
			case "audit":
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return problem }
			case "outbox":
				h.m.events = failingPlanningPublisher{}
			case "confirmed":
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("state", "CONFIRMED").Error)
			case "missing draft":
				in.OccurredAt = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
				h.m.now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }
			}
			var before model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&before).Error)
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }))
			var after model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&after).Error)
			require.Equal(t, before, after, "failure must preserve report, author, version and hash")
		})
	}
}

func TestAutomationReportUsesTriggerPeriodWhenDeliveryIsDelayed(t *testing.T) {
	h, r, ctx, in := automationReportHarness(t)
	h.m.now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }))
	var row model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
	require.Equal(t, r.Version+1, row.Version)
	in.OccurredAt = time.Time{}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) }), ErrInvalid)
}

func TestAutomationReportSourceAndWriteFailuresPreserveDraft(t *testing.T) {
	for _, fault := range []string{"lock", "inactive", "capability", "query", "content", "facts_missing", "state", "write"} {
		t.Run(fault, func(t *testing.T) {
			h, r, ctx, in := automationReportHarness(t)
			switch fault {
			case "lock":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "inactive":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "capability":
				h.m.scheduledFacts.ReadOn = nil
			case "query":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("automation_report_read", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "content":
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("content", "{").Error)
			case "facts_missing":
				in.WorkItemID = 999
			case "state":
				h.facts.Work[1].State = "CANCELLED" //nolint:misspell // Preserve the published domain or serialized contract spelling.
			case "write":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("automation_report_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.writeAutomationReportOn(ctx, tx, in) })
			require.Error(t, err)
			require.NoError(t, h.db.Callback().Query().Remove("automation_report_read"))
			require.NoError(t, h.db.Callback().Update().Remove("automation_report_write"))
			var row model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Take(&row).Error)
			require.Equal(t, r.Version, row.Version)
			require.Equal(t, "DRAFT", row.State)
		})
	}
}
