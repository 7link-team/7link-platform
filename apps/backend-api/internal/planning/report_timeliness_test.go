package planning

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportTimelinessHistoricalCohortMissingFactsAndCurrentScope(t *testing.T) {
	h := setup(t)
	due := time.Date(2026, 10, 19, 23, 59, 0, 0, time.UTC)
	for i, source := range []struct {
		tenant, project, principal guid.ID
		state                      string
		late, undated              bool
	}{
		{1, 3, 2, "SUBMITTED", false, false}, {1, 3, 2, "CONFIRMED", true, false},
		{1, 3, 2, "DRAFT", false, false}, {1, 3, 2, "RETURNED", false, false},
		{1, 3, 2, "SUBMITTED", false, true}, {2, 3, 2, "SUBMITTED", false, false},
		{1, 4, 2, "SUBMITTED", false, false}, {1, 3, 3, "SUBMITTED", false, false},
	} {
		row := model.Report{TenantID: source.tenant, ID: guid.ID(800 + i), ProjectID: source.project, PrincipalID: source.principal, State: source.state, PeriodKind: "DAY", PeriodStart: due.AddDate(0, 0, -i).Format(time.DateOnly), Content: "{}", Facts: "{}", ContentHash: "fact", Version: 1, CreatedBy: source.principal, CreatedAt: due.Add(-time.Hour), DueAt: &due, SubmittedLate: source.late}
		if source.undated {
			row.DueAt = nil
		}
		require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&row).Error)
		if i < 2 {
			at := due.Add(-time.Hour)
			if source.late {
				at = due.Add(time.Hour)
			}
			require.NoError(t, h.db.Table(h.m.names.ReportSubmissions.String()).Create(&model.ReportSubmission{TenantID: row.TenantID, ID: guid.ID(900 + i), ReportID: row.ID, ReportVersion: 2, SubmittedAt: at, DueAt: &due}).Error)
		}
	}
	h.m.entityPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, project guid.ID, _ string, actor guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{actor.String()}}, nil
	}
	var out ReportTimelinessHistory
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.reportTimelinessOn(claims(t, 2), tx, 1, "2026-10-19", "2026-10-21")
		return err
	}))
	require.Len(t, out.Days, 2)
	require.Equal(t, 4, out.Days[0].Due)
	require.Equal(t, 2, out.Days[0].Submitted)
	require.Equal(t, 1, out.Days[0].OnTime)
	require.Equal(t, 1, out.Days[0].Late)
	require.Equal(t, 2, out.Days[0].Unavailable)
	require.Nil(t, out.Days[0].OnTimeRate, "legacy missing immutable facts cannot imply zero or a partial historical rate")
	require.Nil(t, out.Days[1].OnTimeRate, "missing cohort must never appear as zero percent")
	require.Equal(t, 1, out.MissingDeadlines)
	for _, actor := range []guid.ID{6, 7, 8} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.m.reportTimelinessOn(claims(t, actor), tx, 1, "2026-10-19", "2026-10-21")
			return err
		}))
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.reportTimelinessOn(claims(t, 2), tx, 1, "2026-02-30", "2026-10-21")
		return err
	}), ErrInvalid)
}

func TestReportTimelinessImmutableAcrossReturnResubmitAndRollback(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	r := reportDraft(t, h)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &body))
	body.Uncompleted = "Pending work"
	body.Retrospective = "Learn from evidence"
	body.NextPlan = []PlanningPlanItem{linkedItem(goal)}
	body.DeclaredConfidence = "YELLOW"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return err
	}))
	due := h.m.now().Add(time.Hour)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).Update("due_at", due).Error)
	act := func(actor guid.ID, action string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			r, err = h.m.decideReportOn(claims(t, actor), tx, 1, r.ID, PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: action, Reason: "Clarify the evidence"})
			return err
		})
	}
	require.NoError(t, act(2, "SUBMIT"))
	require.NoError(t, act(3, "RETURN"))
	h.m.now = func() time.Time { return due.Add(time.Hour) }
	require.NoError(t, act(2, "SUBMIT"))
	require.True(t, r.SubmittedLate)
	var out ReportTimelinessHistory
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.reportTimelinessOn(claims(t, 2), tx, 1, "2026-10-20", "2026-10-21")
		return err
	}))
	require.Equal(t, 1, out.Days[0].Submitted)
	require.Equal(t, 1, out.Days[0].OnTime)
	require.Equal(t, 0, out.Days[0].Late)
	require.InDelta(t, 1.0, *out.Days[0].OnTimeRate, 0)
	var count int64
	require.NoError(t, h.db.Table(h.m.names.ReportSubmissions.String()).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, act(3, "RETURN"))
	old := r
	abort := errors.New("outbox unavailable")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.decideReportOn(claims(t, 2), tx, 1, r.ID, PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "SUBMIT"})
		if err != nil {
			return err
		}
		return abort
	}), abort)
	require.NoError(t, h.db.Table(h.m.names.ReportSubmissions.String()).Count(&count).Error)
	require.EqualValues(t, 2, count)
	var stored model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("tenant_id=? AND id=?", 1, r.ID).First(&stored).Error)
	require.Equal(t, old.Version, stored.Version)
	require.Equal(t, "RETURNED", stored.State)
}

func TestReportTimelinessHTTPUsesImmutableReceiptsAndRejectsUnavailableSources(t *testing.T) {
	h := setup(t)
	due := h.m.now()
	r := model.Report{TenantID: 1, ID: 810, ProjectID: 3, PrincipalID: 2, State: "SUBMITTED", PeriodKind: "DAY", PeriodStart: "2026-10-20", Content: "{}", Facts: "{}", ContentHash: "fact", Version: 1, CreatedBy: 2, CreatedAt: due, DueAt: &due}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&r).Error)
	wrongDue := due.Add(-time.Hour)
	require.NoError(t, h.db.Table(h.m.names.ReportSubmissions.String()).Create(&model.ReportSubmission{TenantID: 1, ID: 910, ReportID: r.ID, ReportVersion: 2, SubmittedAt: wrongDue, DueAt: &wrongDue}).Error)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	request := func(path string, actor guid.ID) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(claims(t, actor)))
		return rec
	}
	rec := request("/tenants/1/metrics/report-timeliness?from=2026-10-20&to=2026-10-21", 2)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"unavailable":1`)
	require.Contains(t, rec.Body.String(), `"on_time_rate":null`)
	for _, query := range []string{"to=bad", "from=2026-10-21&to=2026-10-20", "from=2026-01-01&to=2026-10-21"} {
		rec = request("/tenants/1/metrics/report-timeliness?"+query, 2)
		require.GreaterOrEqual(t, rec.Code, 400)
	}
	rec = request("/tenants/1/metrics/report-timeliness", 6)
	require.Equal(t, 403, rec.Code)
	for _, table := range []string{h.m.names.Reports.String(), h.m.names.ReportSubmissions.String()} {
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("timeliness_source", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(ErrUnavailable)
			}
		}))
		rec = request("/tenants/1/metrics/report-timeliness?from=2026-10-20&to=2026-10-21", 2)
		require.Equal(t, 503, rec.Code, rec.Body.String())
		require.NoError(t, h.db.Callback().Query().Remove("timeliness_source"))
	}
	h.m.runtime = nil
	rec = request("/tenants/1/metrics/report-timeliness", 2)
	require.Equal(t, 503, rec.Code)
	h.m.ids = nil
	require.ErrorIs(t, h.m.recordReportSubmissionOn(t.Context(), h.db, &r), ErrUnavailable)
	h.m.ids = failingPlanningIDs{err: ErrUnavailable}
	require.ErrorIs(t, h.m.recordReportSubmissionOn(t.Context(), h.db, &r), ErrUnavailable)
}
