package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCollaborationProjectionSourceAndAudienceFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
				if mode == "source" {
					return clientwork.CollaborationNotificationFact{}, false, ErrUnavailable
				}
				return clientwork.CollaborationNotificationFact{ProjectID: 81, State: "ACTIVE", Decision: "ACTIVE", RequesterID: 11}, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.preview_share_decided"}
			switch mode {
			case "identity":
				e.SubjectKind = "unknown"
			case "missing":
				f.m.collaborationFacts = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectCollaborationEventOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	row := &model.Notification{TenantID: 7, Type: "preview.share_decided"}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`} {
		row.Params = raw
		yes, err := f.m.collaborationAudienceOn(t.Context(), f.db, row, 11)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
	row.Params = `{"subject_id":"91"}`
	f.m.collaborationFacts = nil
	yes, err := f.m.collaborationAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestDispatchAppliedUsesActualAutoReceiptAndCurrentLeads(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.CollaborationNotificationFact{ProjectID: 9, WorkItemID: 81, AppliedID: 21, AssignmentID: 101, State: "APPLIED", Automatic: true}
	f.m.collaborationFacts = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, kind string) (clientwork.CollaborationNotificationFact, bool, error) {
		require.Equal(t, "dispatch", kind)
		return fact, true, nil
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.dispatch_decided", Data: json.RawMessage(`{"notify_principal_ids":[11]}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("dispatch.applied")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, 1, rows[0].MergeCount)
	require.Equal(t, "DAILY_DIGEST", defaultEmail(rows[0].Type, Info))
	require.Equal(t, 15*time.Minute, mergeWindow(rows[0].Type))
	for _, state := range []string{"PROPOSED", "DISMISSED", "EXPIRED", "OVERRIDDEN"} {
		fact.State = state
		visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
		require.NoError(t, err)
		require.False(t, visible)
	}
	fact.State = "APPLIED"
	fact.Automatic = false
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible, "a manual decision is not the AUTO notification source")
	fact.Automatic = true
	f.allow = false
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
}

func TestCatalogLocalesContainReadableText(t *testing.T) {
	_ = newFixture(t)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, typ := range []string{"review_sheet_opened", "review_sheet_rejected", "review_sheet_due", "spawn_grant_threshold", "spawn_grant_expiring", "agent_spawn_requested", "planning_goal_at_risk", "preview_share_requested", "preview_share_decided", "project_tolerance_at_risk", "project_constitution_submitted", "work_review_overdue", "project_blocked_long", "dispatch_applied"} {
			for _, suffix := range []string{"_title", "_body", "_email_subject", "_email_text", "_email_html"} {
				text, err := render(i18n.WithLocale(t.Context(), locale), "notification."+typ+suffix, map[string]string{"subject_id": "91", "grant_id": "92", "pct": "80", "spent_micros": "80", "cap_micros": "100"})
				require.NoError(t, err)
				require.NotContains(t, text, "???", locale+":"+typ+suffix)
			}
		}
	}
}

func TestCollaborationProjectionRollsBackWholeAudienceAndTemplatesRender(t *testing.T) {
	f := newFixture(t)
	f.m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
		return clientwork.CollaborationNotificationFact{ProjectID: 9, State: "ACCEPTED", Decision: "ACCEPT"}, true, nil
	}
	f.m.subjectHumanActor = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, string) (guid.ID, bool, error) {
		return 11, true, nil
	}
	failure := errors.New("audience source failed")
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, failure
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.package_decided", Data: json.RawMessage(`{}`)}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), failure)
	q := query.BindNotifications(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count, "recipient 11 insert rolls back when recipient 12 source fails")
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, typ := range []string{"delivery_receipt_received", "delivery_client_rejected", "dispatch_confirmation_required"} {
			ctx := i18n.WithLocale(t.Context(), locale)
			params := map[string]string{"subject_id": "91"}
			for _, suffix := range []string{"_title", "_body"} {
				text, err := render(ctx, "notification."+typ+suffix, params)
				require.NoError(t, err)
				require.NotContains(t, text, "???")
			}
			_, err := f.m.emailPayload(ctx, EmailMessage{To: []mail.Address{{Address: "reviewer@example.test"}}, Locale: locale, SourceRef: "test", TemplateKey: "notification." + typ, Params: params})
			require.NoError(t, err)
		}
	}
}

func TestCollaborationReceiptAudienceUsesPublisherNotDraftCreator(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.CollaborationNotificationFact{ProjectID: 9, State: "REJECTED", Decision: "REJECT"}
	found := true
	var sourceErr error
	f.m.collaborationFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, kind string) (clientwork.CollaborationNotificationFact, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), id)
		require.Equal(t, "package", kind)
		return fact, found, sourceErr
	}
	f.m.subjectHumanActor = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, kind string, id guid.ID, typ string) (guid.ID, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(9), project)
		require.Equal(t, "clientwork", kind)
		require.Equal(t, guid.ID(91), id)
		require.Equal(t, "clientwork.package_published", typ)
		return 12, true, nil
	}
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	f.m.workFacts.ProjectModeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "HYBRID", nil }
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.package_decided", ActorID: 11, Data: json.RawMessage(`{"notify_principal_ids":[11],"reason":"private"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, guid.ID(12), row.RecipientPrincipalID)
	require.Equal(t, "delivery.client_rejected", row.Type)
	require.Equal(t, "/admin/7/entities/9/deliveries?id=91", row.LinkPath)
	require.Equal(t, 1, row.MergeCount)
	require.NotContains(t, row.Params, "private")
	visible, err := f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.True(t, visible)
	f.allow = false
	visible, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, visible, "publisher identity cannot bypass current source access")
	f.allow = true
	fact.ProjectID = 8
	visible, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, visible)
	fact.ProjectID = 9
	found = false
	visible, err = f.m.visibleOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, visible)
	found = true
	sourceErr = errors.New("source failed")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), sourceErr)
}

func TestCollaborationDispatchRequiresPendingHumanSourceAndCurrentAssignAuthority(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.CollaborationNotificationFact{ProjectID: 9, State: "PROPOSED", RequiresHuman: true, ExpiresAt: f.m.now().Add(time.Hour)}
	f.m.collaborationFacts = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, kind string) (clientwork.CollaborationNotificationFact, bool, error) {
		require.Equal(t, "dispatch", kind)
		return fact, true, nil
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12 && r.Permission == "work_item_assign"}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.dispatch_proposed", Data: json.RawMessage(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, guid.ID(12), row.RecipientPrincipalID)
	require.Equal(t, "/admin/7/entities/9/dispatch", row.LinkPath)
	for _, invalid := range []clientwork.CollaborationNotificationFact{
		{ProjectID: 8, State: "PROPOSED", RequiresHuman: true, ExpiresAt: fact.ExpiresAt},
		{ProjectID: 9, State: "APPLIED", RequiresHuman: true, ExpiresAt: fact.ExpiresAt},
		{ProjectID: 9, State: "PROPOSED", RequiresHuman: false, ExpiresAt: fact.ExpiresAt},
		{ProjectID: 9, State: "PROPOSED", RequiresHuman: true, ExpiresAt: f.m.now()},
	} {
		fact = invalid
		visible, err := f.m.visibleOn(t.Context(), f.db, row, 12)
		require.NoError(t, err)
		require.False(t, visible)
	}
}

func TestPreviewNoticesUseIndependentCurrentApproverAndRealRequester(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.CollaborationNotificationFact{ProjectID: 9, State: "PENDING_APPROVAL", RequesterID: 11}
	f.m.collaborationFacts = func(ctx context.Context, _ *gorm.DB, _, _ guid.ID, kind string) (clientwork.CollaborationNotificationFact, bool, error) {
		require.Equal(t, "preview", kind)
		current := fact
		current.ApprovalEligible = authn.PrincipalIDFromContext(ctx) == 12
		return current, true, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.preview_share_requested", Data: json.RawMessage(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, "/admin/7/entities/9/previews?collection=shares&id=91", rows[0].LinkPath)
	require.Equal(t, "OFF", defaultEmail(rows[0].Type, Action))
	fact.State, fact.Decision = "REJECTED", "REJECTED"
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
	e.ID++
	e.Type = "clientwork.preview_share_decided"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("preview.share_decided")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	f.allow = false
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, visible)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, key := range []string{"preview_share_requested", "preview_share_decided", "project_tolerance_at_risk"} {
			_, err = render(i18n.WithLocale(t.Context(), locale), "notification."+key+"_body", map[string]string{"subject_id": "91"})
			require.NoError(t, err)
		}
	}
}
func TestToleranceNoticeRechecksCurrentLeadAndRiskAndDeduplicatesPerDay(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.CollaborationNotificationFact{ProjectID: 9, State: "AT_RISK"}
	f.m.collaborationFacts = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, kind string) (clientwork.CollaborationNotificationFact, bool, error) {
		require.Equal(t, "tolerance", kind)
		return fact, true, nil
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.tolerance_forecasted", Data: json.RawMessage(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, "/admin/7/entities/9/exceptions?id=91", rows[0].LinkPath)
	fact.State = "ON_TRACK"
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
	fact.State = "AT_RISK"
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
}
