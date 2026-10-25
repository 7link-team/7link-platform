package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAggregationHTTPReplaysOnlyWithCurrentAuthorityAndFailsClosed(t *testing.T) {
	h := setup(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	call := func(actor guid.ID, key string, body PlanningAggregationRequest, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/reports/aggregations", strings.NewReader(string(data))).WithContext(claims(t, actor))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, status, response.Code, response.Body.String())
		return response
	}
	body := PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}
	first := call(3, "aggregate-create", body, 201)
	var original, replay struct {
		Data PlanningRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	require.Equal(t, guid.ID(3), original.Data.AuthorID)
	require.NoError(t, json.Unmarshal(call(3, "aggregate-create", body, 201).Body.Bytes(), &replay))
	require.Equal(t, original.Data.ID, replay.Data.ID)
	call(7, "aggregate-agent", body, 403)
	team := body
	team.Scope, team.ProjectID = "TEAM", 0
	call(3, "aggregate-team", team, 503)
	invalid := body
	invalid.PeriodStart = "2026-10-20"
	call(3, "aggregate-invalid-period", invalid, 422)
	h.m.entityPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "report_read"}, nil
	}
	call(3, "aggregate-create", body, 403)
	q := query.BindReports(h.db, h.m.names)
	count, err := q.WithContext(claims(t, 3)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestTeamAggregationCombinesActualProjectFactsAndMissingCapabilities(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	var projects []guid.ID
	h.m.facts.ReadOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, project, _ guid.ID, _, _ time.Time) (delivery.PlanningSnapshot, error) {
		projects = append(projects, project)
		return delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{{ID: project}}, ResultCount: int64(project), HandoffCount: 1, MissingSources: []string{"cost"}}, nil
	}
	h.m.executions.ReadOn = nil
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, err := h.m.reportFactsOn(claims(t, 3), tx, 1, &model.Report{}, h.m.now(), h.m.now().Add(time.Hour))
		require.NoError(t, err)
		require.Equal(t, []guid.ID{3, 4}, projects)
		require.Len(t, facts.Work, 2)
		require.EqualValues(t, 7, facts.ResultCount)
		require.EqualValues(t, 2, facts.HandoffCount)
		require.Nil(t, facts.ExecutionCreated)
		require.Contains(t, facts.MissingSources, "cost")
		return nil
	}))
}

func TestTeamAggregationRetainsExecutionStatesAndPropagatesProjectFailure(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	h.m.executions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (execution.PeriodFacts, error) {
		return execution.PeriodFacts{Created: 2, States: map[string]int64{"SUCCEEDED": 1, "FAILED": 1}}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, err := h.m.reportFactsOn(claims(t, 3), tx, 1, &model.Report{}, h.m.now(), h.m.now().Add(time.Hour))
		require.NoError(t, err)
		require.EqualValues(t, 4, *facts.ExecutionCreated)
		require.Equal(t, map[string]int64{"SUCCEEDED": 2, "FAILED": 2}, facts.ExecutionStates)
		require.Len(t, facts.Work, 2, "a work item must appear only once")
		return nil
	}))
	failure := errors.New("delivery connection lost")
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{}, failure
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.reportFactsOn(claims(t, 3), tx, 1, &model.Report{}, h.m.now(), h.m.now().Add(time.Hour))
		return err
	}), failure)
}

func TestAggregateAuditFailureRollsBackDraftAndAudit(t *testing.T) {
	h := setup(t)
	failure := errors.New("audit unavailable")
	h.m.audit = func(ctx context.Context, tx *gorm.DB, _ audit.DomainEvent) error {
		require.NoError(t, tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", "aggregate").Error)
		return failure
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		return err
	}), failure)
	q := query.BindReports(h.db, h.m.names)
	count, err := q.WithContext(claims(t, 3)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	var auditCount int64
	require.NoError(t, h.db.Table("audit_probe").Count(&auditCount).Error)
	require.Zero(t, auditCount)
}

func TestReportChildReferencesRejectDuplicateUnknownAndRevokedChildren(t *testing.T) {
	h := setup(t)
	child := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-19"})
	parent := reportDraft(t, h)
	q := query.BindReports(h.db, h.m.names)
	row, err := q.WithContext(claims(t, 2)).Where(q.ID.Eq(parent.ID)).First()
	require.NoError(t, err)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(parent.Content, &body))
	check := func(refs []PlanningChildReportRef, want error) {
		t.Helper()
		body.Children = refs
		row.Content, _, err = hashJSON(body)
		require.NoError(t, err)
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			return h.m.checkReportChildrenOn(claims(t, 2), tx, 1, row, &PlanningCheck{Result: "PASS"})
		}), want)
	}
	ref := PlanningChildReportRef{ID: child.ID, ContentHash: child.ContentHash}
	check([]PlanningChildReportRef{ref, ref}, ErrInvalid)
	check([]PlanningChildReportRef{ref, {ID: 999, ContentHash: "unknown"}}, ErrConflict)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	check([]PlanningChildReportRef{ref}, ErrForbidden)
}

func TestAggregateCompletenessBoundsNeverSilentlyTruncateFactsOrChildren(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	h.facts.Work = make([]delivery.PlanningWorkFact, 501)
	for i := range h.facts.Work {
		h.facts.Work[i].ID = guid.ID(i + 1)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.reportFactsOn(claims(t, 3), tx, 1, &model.Report{}, h.m.now(), h.m.now().Add(time.Hour))
		return err
	}), ErrUnavailable)
	rows := make([]*model.Report, 501)
	for i := range rows {
		rows[i] = &model.Report{TenantID: 1, ID: guid.ID(1000 + i), ProjectID: 3, PrincipalID: guid.ID(100 + i), PeriodKind: "WEEK", PeriodStart: "2026-10-19", State: "DRAFT", Content: "{}", ContentHash: strings.Repeat("a", 64), Facts: "{}", Version: 1, CreatedBy: 3, CreatedAt: h.m.now(), UpdatedAt: h.m.now()}
	}
	q := query.BindReports(h.db, h.m.names)
	require.NoError(t, q.WithContext(claims(t, 3)).Create(rows...))
	parent := &model.Report{ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Content: "{}"}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.childReportsOn(claims(t, 3), tx, 1, parent); return err }), ErrUnavailable)
	_, err := q.WithContext(claims(t, 3)).Where(q.ID.Eq(rows[500].ID)).Delete()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		children, err := h.m.childReportsOn(claims(t, 3), tx, 1, parent)
		require.NoError(t, err)
		require.Len(t, children, 500)
		return nil
	}))
}

func TestAggregateFactFailureLeavesNoDraft(t *testing.T) {
	h := setup(t)
	failure := errors.New("project facts unavailable")
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "TEAM", PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		return err
	}), failure)
	q := query.BindReports(h.db, h.m.names)
	count, err := q.WithContext(claims(t, 3)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestAggregateAllocationAndChildReadFailuresLeaveNoPartialAggregate(t *testing.T) {
	h := setup(t)
	reportDraft(t, h)
	input := PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}
	ids := h.m.ids
	failure := errors.New("ID allocator unavailable")
	h.m.ids = failingPlanningIDs{err: failure}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createAggregationOn(claims(t, 3), tx, 1, input); return err }), failure)
	h.m.ids = ids
	h.m.entityPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "report_submit"}, nil
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createAggregationOn(claims(t, 3), tx, 1, input); return err }), ErrForbidden)
	q := query.BindReports(h.db, h.m.names)
	count, err := q.WithContext(claims(t, 3)).Where(q.PrincipalID.Eq(guid.ID(0))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestAggregateNextPlansRequireSameLevelActiveGoalsAndVisibleWork(t *testing.T) {
	h := setup(t)
	member := goalTree(t, h)
	var memberGoal PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &memberGoal))
	project := &model.Report{ProjectID: 3}
	item := PlanningPlanItem{Title: "Follow up", GoalID: *memberGoal.ParentGoalID, KR: "delivery", Category: "OTHER"}
	check := func(report *model.Report, items []PlanningPlanItem, want error) {
		t.Helper()
		err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateReportItems(claims(t, 3), tx, 1, report, items) })
		if want == nil {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, want)
		}
	}
	check(project, []PlanningPlanItem{item}, nil)
	check(project, nil, ErrInvalid)
	for _, change := range []func(*PlanningPlanItem){
		func(v *PlanningPlanItem) { v.Title = "" },
		func(v *PlanningPlanItem) { v.KR = "missing" },
		func(v *PlanningPlanItem) { v.GoalID = member.ID },
		func(v *PlanningPlanItem) { v.Category = "INVALID" },
		func(v *PlanningPlanItem) { id := guid.ID(999); v.WorkItemID = &id },
	} {
		bad := item
		change(&bad)
		check(project, []PlanningPlanItem{bad}, ErrInvalid)
	}
	linked := item
	work := guid.ID(100)
	linked.WorkItemID, linked.Category = &work, ""
	check(project, []PlanningPlanItem{linked}, nil)
	var projectGoal PlanningGoalInput
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, g, err := h.m.goalOn(claims(t, 3), tx, 1, 3, item.GoalID)
		projectGoal = g
		return err
	}))
	teamItem := item
	teamItem.GoalID = *projectGoal.ParentGoalID
	check(&model.Report{}, []PlanningPlanItem{teamItem}, nil)
	teamItem.WorkItemID = &work
	check(&model.Report{}, []PlanningPlanItem{teamItem}, ErrInvalid)
	q := query.BindGoals(h.db, h.m.names)
	_, err := q.WithContext(claims(t, 3)).Where(q.ID.Eq(item.GoalID)).Update(q.State, "ACHIEVED")
	require.NoError(t, err)
	check(project, []PlanningPlanItem{item}, ErrInvalid)
}

func TestAggregateReportsUseLiveFactsChildProvenanceAndIndependentHuman(t *testing.T) {
	h := setup(t)
	// The project lead authors this aggregate; its independent confirmer is
	// the team's explicitly configured current Human owner.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{TeamOwnerID: settingPtr(guid.ID(4))})
		return err
	}))
	memberGoal := goalTree(t, h)
	child := reportDraft(t, h)
	var mg PlanningGoalInput
	require.NoError(t, json.Unmarshal(memberGoal.Content, &mg))
	var aggregate PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		return err
	}))
	require.Zero(t, aggregate.PrincipalID)
	require.Equal(t, guid.ID(3), aggregate.AuthorID)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(aggregate.Content, &body))
	require.Equal(t, "PROJECT", body.Scope)
	require.Len(t, body.Completed, 1)
	require.Equal(t, guid.ID(100), body.Completed[0].WorkItemID)
	require.Equal(t, []PlanningChildReportRef{{ID: child.ID, ContentHash: child.ContentHash}}, body.Children)
	require.Equal(t, time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC), body.BoundaryStart.UTC())
	body.Uncompleted = "Current work remains"
	body.Retrospective = "Use accepted results"
	body.DeclaredConfidence = "YELLOW"
	body.NextPlan = []PlanningPlanItem{{Title: "Project follow-up", GoalID: *mg.ParentGoalID, KR: "delivery", Category: "OTHER"}}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.editReportOn(claims(t, 2), tx, 1, aggregate.ID, aggregate.Version, body)
		return err
	}), ErrForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = h.m.editReportOn(claims(t, 3), tx, 1, aggregate.ID, aggregate.Version, body)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReports(tx, h.m.names)
		row, err := q.WithContext(claims(t, 3)).Where(q.ID.Eq(aggregate.ID)).First()
		if err != nil {
			return err
		}
		check, err := h.m.checkReport(claims(t, 3), tx, 1, row)
		require.NoError(t, err)
		require.Equal(t, "PASS_WITH_FLAGS", check.Result)
		require.Contains(t, check.Issues, PlanningIssue{Code: "N1_CHILD_UNSUBMITTED"})
		return nil
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = h.m.decideReportOn(claims(t, 3), tx, 1, aggregate.ID, PlanningDecisionInput{Action: "SUBMIT", Version: aggregate.Version, ExpectedContentHash: aggregate.ContentHash})
		return err
	}))
	decision := PlanningDecisionInput{Action: "CONFIRM", Version: aggregate.Version, ExpectedContentHash: aggregate.ContentHash}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.decideReportOn(claims(t, 3), tx, 1, aggregate.ID, decision)
		return err
	}), ErrForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = h.m.decideReportOn(claims(t, 4), tx, 1, aggregate.ID, decision)
		return err
	}))
	require.Equal(t, "CONFIRMED", aggregate.State)
	require.Equal(t, guid.ID(4), *aggregate.ConfirmedBy)
}
func TestAggregateMismatchAndTeamUnavailableNeverBecomeSuccess(t *testing.T) {
	h := setup(t)
	child := reportDraft(t, h)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "TEAM", PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		return err
	}), ErrUnavailable)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		project, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		if err != nil {
			return err
		}
		team, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "TEAM", PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		if err != nil {
			return err
		}
		require.Len(t, team.Facts.Work, 2)
		require.EqualValues(t, 2, team.Facts.ResultCount)
		var content PlanningReportInput
		require.NoError(t, json.Unmarshal(team.Content, &content))
		require.Equal(t, project.ID, content.Children[0].ID)
		q := query.BindReports(tx, h.m.names)
		var childBody PlanningReportInput
		require.NoError(t, json.Unmarshal(child.Content, &childBody))
		childBody.Completed = append(childBody.Completed, PlanningCompletion{WorkItemID: 999})
		body, hash, err := hashJSON(childBody)
		require.NoError(t, err)
		_, err = q.WithContext(claims(t, 3)).Where(q.ID.Eq(child.ID)).Updates(map[string]any{"state": "SUBMITTED", "content": body, "content_hash": hash})
		require.NoError(t, err)
		row, err := q.WithContext(claims(t, 3)).Where(q.ID.Eq(project.ID)).First()
		require.NoError(t, err)
		check, err := h.m.checkReport(claims(t, 3), tx, 1, row)
		require.NoError(t, err)
		require.Equal(t, "BLOCKED", check.Result)
		require.Contains(t, check.Issues, PlanningIssue{Code: "B2_AGGREGATE_MISMATCH", Blocking: true})
		return nil
	}))
	for _, in := range []PlanningAggregationRequest{{Scope: "TEAM", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}, {Scope: "PROJECT", PeriodKind: "WEEK", PeriodStart: "2026-10-19"}} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createAggregationOn(claims(t, 3), tx, 1, in); return err }), ErrInvalid)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createAggregationOn(claims(t, 7), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "MONTH", PeriodStart: "2026-10-01"})
		return err
	}), ErrForbidden)
}
func TestReportBoundariesRemainFixedAfterSettingsChangeAndAuthorEdit(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	var content PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &content))
	start, end := *content.BoundaryStart, *content.BoundaryEnd
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{Timezone: settingPtr("America/Los_Angeles")})
		return err
	}))
	content.BoundaryStart = nil
	content.BoundaryEnd = nil
	content.Scope = "TEAM"
	content.Children = []PlanningChildReportRef{{ID: 123, ContentHash: "fake"}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, content)
		return err
	}))
	content = PlanningReportInput{}
	require.NoError(t, json.Unmarshal(r.Content, &content))
	require.True(t, start.Equal(*content.BoundaryStart))
	require.True(t, end.Equal(*content.BoundaryEnd))
	require.Equal(t, "MEMBER", content.Scope)
	require.Empty(t, content.Children)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReports(tx, h.m.names)
		row, err := q.WithContext(claims(t, 2)).Where(q.ID.Eq(r.ID)).First()
		require.NoError(t, err)
		a, b, err := reportPeriod(row)
		require.NoError(t, err)
		require.True(t, start.Equal(a))
		require.True(t, end.Equal(b))
		return nil
	}))
}

func TestTimeAggregationLinksLowerPeriodsAndRechecksCurrentChildClaims(t *testing.T) {
	h := setup(t)
	day := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-19"})
	week := reportDraft(t, h)
	var content PlanningReportInput
	require.NoError(t, json.Unmarshal(week.Content, &content))
	require.Equal(t, []PlanningChildReportRef{{ID: day.ID, ContentHash: day.ContentHash}}, content.Children)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReports(tx, h.m.names)
		var lower PlanningReportInput
		require.NoError(t, json.Unmarshal(day.Content, &lower))
		lower.Completed = append(lower.Completed, PlanningCompletion{WorkItemID: 999})
		body, hash, err := hashJSON(lower)
		require.NoError(t, err)
		_, err = q.WithContext(claims(t, 2)).Where(q.ID.Eq(day.ID)).Updates(map[string]any{"state": "SUBMITTED", "content": body, "content_hash": hash})
		require.NoError(t, err)
		row, err := q.WithContext(claims(t, 2)).Where(q.ID.Eq(week.ID)).First()
		require.NoError(t, err)
		check, err := h.m.checkReport(claims(t, 2), tx, 1, row)
		require.NoError(t, err)
		require.Contains(t, check.Issues, PlanningIssue{Code: "B2_AGGREGATE_MISMATCH", Blocking: true})
		require.Contains(t, check.Issues, PlanningIssue{Code: "N4_CHILD_CHANGED"})
		return nil
	}))
}
func TestCrossMonthWeekUsesAcceptanceDateRatherThanWholeChildTotal(t *testing.T) {
	h := setup(t)
	accepted := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	h.facts.Work[0].AcceptedAt = &accepted
	week := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-26"})
	month := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-11-01"})
	var content PlanningReportInput
	require.NoError(t, json.Unmarshal(month.Content, &content))
	require.Empty(t, content.Completed)
	require.Len(t, content.Children, 1)
	require.Equal(t, week.ID, content.Children[0].ID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReports(tx, h.m.names)
		_, err := q.WithContext(claims(t, 2)).Where(q.ID.Eq(week.ID)).Update(q.State, "SUBMITTED")
		require.NoError(t, err)
		row, err := q.WithContext(claims(t, 2)).Where(q.ID.Eq(month.ID)).First()
		require.NoError(t, err)
		check, err := h.m.checkReport(claims(t, 2), tx, 1, row)
		require.NoError(t, err)
		for _, issue := range check.Issues {
			require.NotEqual(t, "B2_AGGREGATE_MISMATCH", issue.Code)
		}
		return nil
	}))
}
