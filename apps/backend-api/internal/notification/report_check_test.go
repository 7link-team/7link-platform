package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestReportCheckNoticeUsesActualReceiptVersionAndCurrentAuthor(t *testing.T) {
	f := newFixture(t)
	blocked := true
	f.m.planningFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
		return PlanningNotificationFact{ProjectID: 81, State: "DRAFT", LinkPath: "/admin/7/entities/81/reports?record_id=91"}, true, nil
	}
	f.m.planningFacts.CheckedOn = func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID, version int) (bool, error) {
		return blocked && actor == 11 && version == 2, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "planning_report", Type: "planning.report_checked", Data: []byte(`{"result":"BLOCKED","report_version":2}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	blocked = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestOverdueNoticeDelegatesExactCurrentAudienceAndRejectsMalformedSource(t *testing.T) {
	f := newFixture(t)
	current := guid.ID(12)
	f.m.planningFacts.EligibleOn = func(_ context.Context, _ *gorm.DB, tenant, id, actor guid.ID, kind, state string) (bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), id)
		require.Equal(t, "report", kind)
		require.Equal(t, "OVERDUE_AUDIENCE", state)
		return actor == 11 || actor == current, nil
	}
	row := &model.Notification{TenantID: 7, Params: `{"report_id":"91"}`}
	yes, err := f.m.reportOverdueAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.True(t, yes)
	current = 13
	yes, err = f.m.reportOverdueAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, yes)
	row.Params = `{"report_id":"invalid"}`
	_, err = f.m.reportOverdueAudienceOn(t.Context(), f.db, row, 12)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestReportCheckedProjectionFailuresDoNotInventBlockedReceipt(t *testing.T) {
	for _, mode := range []string{"identity", "result", "version", "missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "planning_report"}
			data := map[string]json.RawMessage{"result": []byte(`"BLOCKED"`), "report_version": []byte(`2`)}
			f.m.planningFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error) {
				if mode == "source" {
					return PlanningNotificationFact{}, false, ErrUnavailable
				}
				return PlanningNotificationFact{ProjectID: 81, LinkPath: "/admin/7/entities/81/reports"}, true, nil
			}
			f.m.planningFacts.CheckedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (bool, error) { return true, nil }
			switch mode {
			case "identity":
				e.SubjectID = 0
			case "result":
				data["result"] = []byte(`2`)
			case "version":
				data["report_version"] = []byte(`0`)
			case "missing":
				f.m.planningFacts.FactsOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectReportCheckedOn(t.Context(), tx, e, data) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	f.m.planningFacts.CheckedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (bool, error) {
		return false, ErrUnavailable
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91","report_version":"0"}`, `{"subject_id":"91","report_version":"2"}`} {
		yes, err := f.m.reportCheckedAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Params: raw}, 11)
		require.False(t, yes)
		require.Error(t, err)
	}
	f.m.planningFacts.CheckedOn = nil
	yes, err := f.m.reportCheckedAudienceOn(t.Context(), f.db, &model.Notification{}, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.planningFacts.EligibleOn = nil
	yes, err = f.m.reportOverdueAudienceOn(t.Context(), f.db, &model.Notification{}, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
}
