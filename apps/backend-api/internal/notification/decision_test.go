package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConstitutionProjectionFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "candidates", "order", "visibility", "source", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "project_constitution", Type: "project.constitution_submitted"}
			f.m.decisionFacts.ConstitutionEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
				if mode == "source" {
					return 0, false, ErrUnavailable
				}
				return 81, true, nil
			}
			switch mode {
			case "identity":
				e.ProjectID = 0
			case "missing":
				f.m.candidates = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectConstitutionOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	row := &model.Notification{TenantID: 7, Type: "project.constitution_submitted"}
	f.m.decisionFacts.ConstitutionEligibleOn = nil
	yes, err := f.m.decisionAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.decisionFacts.ConstitutionEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
		return 0, false, ErrUnavailable
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91"}`} {
		row.Params = raw
		yes, err = f.m.decisionAudienceOn(t.Context(), f.db, row, 11)
		require.False(t, yes)
		require.Error(t, err)
	}
}

func TestDecisionProjectionAudienceReplayAndCurrentVisibility(t *testing.T) {
	f := newFixture(t)
	eligible, exists := true, true
	var sourceErr error
	f.m.decisionFacts = delivery.DecisionNotifications{
		FactsOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, kind string) (delivery.DecisionNotificationFact, bool, error) {
			require.Equal(t, guid.ID(7), tenant)
			require.Equal(t, guid.ID(91), id)
			require.Equal(t, "plan", kind)
			return delivery.DecisionNotificationFact{ProjectID: 9, WorkItemID: 81, AuthorID: 11}, exists, sourceErr
		},
		PlanEligibleOn: func(_ context.Context, _ *gorm.DB, _, _, principal guid.ID) (bool, error) {
			return eligible && principal == 12, nil
		},
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "plan.proposed", ActorID: 11, Data: json.RawMessage(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(12), rows[0].RecipientPrincipalID)
	require.Equal(t, "/admin/7/entities/9/plan-review?plan_id=91", rows[0].LinkPath)
	require.Equal(t, 1, rows[0].MergeCount)
	eligible = false
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible, "eligibility is rechecked after projection")
	e.Type, e.ID = "plan.approved", 1002
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq(e.Type)).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID, "actual author, not event actor")
	exists = false
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, visible)
	exists, sourceErr = true, errors.New("source unavailable")
	e.ID, e.Type = 1003, "plan.rejected"
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), sourceErr)
	count, err := q.WithContext(t.Context()).Where(q.Type.Eq(e.Type)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestDecisionRoutesSuppressOrdinaryInternalFailure(t *testing.T) {
	for _, typ := range []string{"internal_qa.failed", "verification.completed", "work_item.escalated"} {
		_, ok := eventRoute(events.RecordedEvent{Type: typ}, map[string]json.RawMessage{"result": json.RawMessage(`"FAILED"`)})
		require.False(t, ok, typ)
	}
	r, ok := eventRoute(events.RecordedEvent{Type: "work_item.escalated"}, map[string]json.RawMessage{"reason_code": json.RawMessage(`"INTERNAL_ROUNDS_EXHAUSTED"`)})
	require.True(t, ok)
	require.Equal(t, Action, r.severity)
}

func TestEscalationUsesCurrentAccountableHuman(t *testing.T) {
	f := newFixture(t)
	f.m.decisionFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (delivery.DecisionNotificationFact, bool, error) {
		return delivery.DecisionNotificationFact{ProjectID: 9, AccountableID: 12}, true, nil
	}
	row := &model.Notification{TenantID: 7, ProjectID: 9, Type: "work_item.escalated", Params: `{"subject_id":"91"}`}
	allowed, err := f.m.decisionAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.True(t, allowed)
	row.ProjectID = 10
	allowed, err = f.m.decisionAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestConstitutionNoticeUsesCurrentHumanReviewAuthority(t *testing.T) {
	f := newFixture(t)
	pending := true
	f.m.decisionFacts.ConstitutionEligibleOn = func(_ context.Context, _ *gorm.DB, tenant, id, actor guid.ID) (guid.ID, bool, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, id)
		return 9, pending && actor == 12, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "project_constitution", Type: "project.constitution_submitted", Data: []byte(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, "/admin/7/entities/9/project?view=constitution", rows[0].LinkPath)
	require.Equal(t, "OFF", defaultEmail(e.Type, Action))
	pending = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, yes)
}
