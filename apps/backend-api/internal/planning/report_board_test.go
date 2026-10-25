package planning

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportBoardFiltersCurrentAuthorityAndPeriod(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	read := func(in ReportBoardInput) (ReportBoard, error) {
		var out ReportBoard
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.reportBoardOn(claims(t, 3), tx, 1, &in)
			return err
		})
		return out, err
	}
	out, err := read(ReportBoardInput{Start: "2026-10-01", End: "2026-11-01", PeriodKind: "WEEK", Origin: "HUMAN"})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	require.Equal(t, r.ID, out.Items[0].Report.ID)
	require.Equal(t, "BLOCKED", out.Items[0].CheckResult)
	out, err = read(ReportBoardInput{Start: "2026-11-01"})
	require.NoError(t, err)
	require.Empty(t, out.Items)
	_, err = read(ReportBoardInput{Start: "2026-11-01", End: "2026-10-01"})
	require.ErrorIs(t, err, ErrInvalid)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	out, err = read(ReportBoardInput{})
	require.NoError(t, err)
	require.Empty(t, out.Items)
}

func TestReportCalendarIncludesMembersWithoutGeneratedDrafts(t *testing.T) {
	h := setup(t)
	h.m.notificationCandidates = identity.NotificationCandidates(func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{2, 3}, nil })
	var out ReportBoard
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.reportBoardOn(claims(t, 3), tx, 1, &ReportBoardInput{})
		return err
	}))
	require.Empty(t, out.Items)
	require.Equal(t, []ReportBoardMember{{PrincipalID: 2, Origin: "HUMAN"}, {PrincipalID: 3, Origin: "HUMAN"}}, out.Members)
}

func TestBatchReportRemindersAreAtomicAndNeverAlterReport(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	remind := func(ids []guid.ID) error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.remindReportsOn(claims(t, 3), tx, 1, ids); return err })
	}
	require.ErrorIs(t, remind([]guid.ID{r.ID, r.ID}), ErrInvalid)
	var count int64
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	require.Zero(t, count)
	h.m.reportNotifier = planningTestNotifier{fail: true}
	require.Error(t, remind([]guid.ID{r.ID}))
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	require.Zero(t, count)
	h.m.reportNotifier = planningTestNotifier{}
	require.NoError(t, remind([]guid.ID{r.ID}))
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		v, err := h.m.readOn(claims(t, 3), tx, 1, r.ID, "report")
		require.Equal(t, r.State, v.State)
		require.Equal(t, r.Version, v.Version)
		require.Equal(t, r.ContentHash, v.ContentHash)
		return err
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.remindReportsOn(claims(t, 6), tx, 1, []guid.ID{r.ID})
		return err
	}), ErrForbidden)
}

func TestReportReminderReplayDoesNotResendAndRechecksCurrentAuthority(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	body := ReportReminderRequest{ReportIDs: []guid.ID{r.ID}}
	command := func() error {
		_, err := h.m.command(claims(t, 3), 1, "manual-report-reminder", "report.remind", "/reports/reminders", body, func(tx *gorm.DB) (PlanningRecord, error) {
			return h.m.remindReportsOn(claims(t, 3), tx, 1, body.ReportIDs)
		})
		return err
	}
	require.NoError(t, command())
	require.NoError(t, command())
	var count int64
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, command())
	require.NoError(t, h.db.Table("planning_notice_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestReportBoardCurrentCandidatesHandleMissingAgentAndSourceFailures(t *testing.T) {
	for _, fault := range []string{"agent", "missing", "missing_capability", "agent_error", "human_error", "candidates_error", "unordered", "filter", "report_read", "settings_read", "start", "end"} {
		t.Run(fault, func(t *testing.T) {
			h := setup(t)
			h.m.notificationCandidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if fault == "candidates_error" {
					return nil, ErrUnavailable
				}
				if fault == "unordered" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{7}, nil
			}
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "agent_error" {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
				return fleet.AgentIdentity{PrincipalID: 7, State: "ACTIVE", Origin: "HUMAN_CREATED"}, fault != "missing", nil
			}
			if fault == "missing_capability" {
				h.m.agents.ByPrincipalOn = nil
			}
			if fault == "human_error" {
				old := h.m.principals.ActiveHumanByID
				h.m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 7 {
						return identity.Principal{}, false, ErrUnavailable
					}
					return old(ctx, tx, id)
				}
			}
			if fault == "report_read" || fault == "settings_read" {
				target := h.m.names.Reports.String()
				if fault == "settings_read" {
					target = h.m.names.Settings.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("board_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			in := ReportBoardInput{}
			if fault == "filter" {
				in.Origin = "HUMAN"
			}
			if fault == "start" {
				in.Start = "bad"
			}
			if fault == "end" {
				in.End = "bad"
			}
			var out ReportBoard
			err := h.db.Transaction(func(tx *gorm.DB) error { var e error; out, e = h.m.reportBoardOn(claims(t, 3), tx, 1, &in); return e })
			if fault == "agent" {
				require.NoError(t, err)
				require.Equal(t, []ReportBoardMember{{PrincipalID: 7, Origin: "HUMAN_CREATED"}}, out.Members)
			} else if fault == "missing" || fault == "missing_capability" || fault == "filter" {
				require.NoError(t, err)
				require.Empty(t, out.Members)
			} else {
				require.Error(t, err)
				require.Empty(t, out.Items)
			}
			require.NoError(t, h.db.Callback().Query().Remove("board_read_failure"))
		})
	}
}

func TestReportBoardHTTPAndRemindersUseCurrentAuthorityWithoutStateChange(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	sink := &reminderNotifier{}
	h.m.reportNotifier = sink
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	get := func(actor guid.ID) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tenants/1/reports/board", nil).WithContext(claims(t, actor)))
		return rec
	}
	rec := get(3)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), r.ID.String())
	rec = get(6)
	require.Equal(t, 403, rec.Code)
	req := httptest.NewRequest(http.MethodPost, "/tenants/1/reports/reminders", strings.NewReader(`{"report_ids":["`+r.ID.String()+`"]}`)).WithContext(claims(t, 3))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "remind-draft")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Len(t, sink.notices, 1)
	require.Equal(t, []guid.ID{2}, sink.notices[0].Recipients)
	for _, ids := range [][]guid.ID{nil, make([]guid.ID, 101), {r.ID, r.ID}, {0}} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.remindReportsOn(claims(t, 3), tx, 1, ids); return e }))
	}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("state", "SUBMITTED").Error)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.remindReportsOn(claims(t, 3), tx, 1, []guid.ID{r.ID}); return e }), ErrConflict)
	h.m.reportNotifier = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.remindReportsOn(claims(t, 3), tx, 1, []guid.ID{r.ID}); return e }), ErrUnavailable)
	h.m.runtime = nil
	rec = get(3)
	require.Equal(t, 503, rec.Code)
}
