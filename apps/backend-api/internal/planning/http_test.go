package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/planning/model"
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
)

func TestHTTPPlanLimitsReturnSpecificDomainCodeWithoutWrites(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct {
		kind, date string
		count      int
	}{{"DAY", "2026-10-20", 31}, {"WEEK", "2026-10-19", 201}} {
		items := make([]PlanningPlanItem, tc.count)
		for i := range items {
			items[i] = linkedItem(g)
		}
		body, err := json.Marshal(PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: tc.kind, PeriodStart: tc.date, Plan: &PlanningPlanInput{Items: items}})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/plans", strings.NewReader(string(body))).WithContext(claims(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "limit-"+tc.kind)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 422, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "180413")
	}
	var count int64
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestMyPlansIncludesProjectsAndRechecksEachProject(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	in := &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 10}
	page, err := h.m.listHTTP(claims(t, 2), "plan", in)
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, p.ID, page.Body.Data.Items[0].ID)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	page, err = h.m.listHTTP(claims(t, 2), "plan", in)
	require.NoError(t, err)
	require.Empty(t, page.Body.Data.Items, "tenant permission cannot replace current project permission")
}

func TestReplayChecksExactAuthorityAndHumanDecisionBoundary(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	r := reportDraft(t, h)
	for _, tc := range []struct {
		name       string
		record     PlanningRecord
		operation  string
		body       any
		actor      guid.ID
		permission string
	}{
		{"member goal", g, "goal.change", PlanningGoalChange{Action: "MANUAL"}, 2, "goal_member_manage"},
		{"plan author", p, "plan.edit", PlanningPlanInput{}, 2, "plan_submit"},
		{"plan submit", p, "plan.decide", PlanningDecisionInput{Action: "SUBMIT"}, 2, "plan_submit"},
		{"plan reviewer", p, "plan.decide", PlanningDecisionInput{Action: "CONFIRM"}, 3, "plan_confirm"},
		{"report author", r, "report.edit", PlanningReportInput{}, 2, "report_submit"},
		{"report reviewer", r, "report.decide", PlanningDecisionInput{Action: "RETURN"}, 3, "report_confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denied := false
			h.m.entityPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: !denied || permission != tc.permission}, nil
			}
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.authorizeReplay(claims(t, tc.actor), tx, 1, tc.record, tc.operation, tc.body)
			}))
			denied = true
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.authorizeReplay(claims(t, tc.actor), tx, 1, tc.record, tc.operation, tc.body)
			}), ErrForbidden)
		})
	}
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeReplay(claims(t, 3), tx, 1, p, "plan.decide", struct{}{}) }), ErrInvalid)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.authorizeReplay(claims(t, 7), tx, 1, g, "goal.change", PlanningGoalChange{Action: "ACTIVATE"})
	}), ErrForbidden)
	missing := g
	missing.ID = 999
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.authorizeReplay(claims(t, 2), tx, 1, missing, "goal.change", PlanningGoalChange{Action: "MANUAL"})
	}), ErrNotFound)
}

func TestMyPlansFiltersBeforePagingAndSupportsEntityOnlyAuthority(t *testing.T) {
	h := setup(t)
	for _, id := range []guid.ID{1, 2, 3, 4} {
		project := guid.ID(3)
		if id == 1 || id == 3 {
			project = 4
		}
		row := model.Plan{TenantID: 1, ID: id, ProjectID: project, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-" + id.String(), State: "DRAFT", Content: "{\"items\":[]}", ContentHash: strings.Repeat("a", 64), Version: 1}
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&row).Error)
	}
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	for _, sort := range []string{"id", "-id"} {
		in := &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: sort}
		first, e := h.m.listHTTP(claims(t, 2), "plan", in)
		require.NoError(t, e)
		require.Len(t, first.Body.Data.Items, 1)
		require.True(t, first.Body.Data.HasMore)
		require.Equal(t, guid.ID(3), first.Body.Data.Items[0].ProjectID)
		in.Cursor = first.Body.Data.NextCursor
		second, e := h.m.listHTTP(claims(t, 2), "plan", in)
		require.NoError(t, e)
		require.Len(t, second.Body.Data.Items, 1)
		require.False(t, second.Body.Data.HasMore)
		require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
	}
	for _, actor := range []guid.ID{8, 9} {
		_, e := h.m.listHTTP(claims(t, actor), "plan", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1})
		require.Error(t, e)
	}
}

func TestScopedListsLoadRecordsInOneQuery(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	reportDraft(t, h)
	counts := map[string]int{}
	callback := "planning_list_query_count"
	require.NoError(t, h.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) { counts[tx.Statement.Table]++ }))
	t.Cleanup(func() { require.NoError(t, h.db.Callback().Query().Remove(callback)) })
	for _, kind := range []string{"goal", "plan", "report"} {
		counts = map[string]int{}
		page, e := h.m.listHTTP(claims(t, 2), kind, &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "3", Limit: 20})
		require.NoError(t, e)
		require.NotEmpty(t, page.Body.Data.Items)
		require.Equal(t, 1, counts["p_planning_"+kind+"s"], "list must not reload every selected record")
	}
}

func TestCommandReplayRequiresOriginalMutationPermissionEvenWhenReadRemains(t *testing.T) {
	h := setup(t)
	body := PlanningWriteInput{PrincipalID: 2, PeriodKind: "QUARTER", PeriodStart: "2026-10-01", Goal: &PlanningGoalInput{Level: "TEAM", Title: "Versioned objective", KeyResults: []PlanningKeyResult{{Key: "k", Title: "Result", Source: "MANUAL", Target: 2}}}}
	fn := func(tx *gorm.DB) (PlanningRecord, error) { return h.m.createOn(claims(t, 2), tx, 1, "goal", body) }
	first, e := h.m.command(claims(t, 2), 1, "mutation-permission", "goal.create", "/goals", body, fn)
	require.NoError(t, e)
	h.m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "goal_read"}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.readOn(claims(t, 2), tx, 1, first.Body.Data.ID, "goal")
		return err
	}))
	_, e = h.m.command(claims(t, 2), 1, "mutation-permission", "goal.create", "/goals", body, func(*gorm.DB) (PlanningRecord, error) {
		t.Fatal("replay executed mutation")
		return PlanningRecord{}, nil
	})
	var status huma.StatusError
	require.ErrorAs(t, e, &status)
	require.Equal(t, 403, status.GetStatus())
}

func TestHTTPCommandsExerciseRealDraftToDecisionLifecycle(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	call := func(actor guid.ID, method, path, key string, body any, status int) PlanningRecord {
		t.Helper()
		b, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, "/tenants/1"+path, strings.NewReader(string(b))).WithContext(claims(t, actor))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		var envelope struct {
			Data PlanningRecord `json:"data"`
		}
		if status < 300 {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		}
		return envelope.Data
	}
	p := call(2, "POST", "/plans", "create-plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(goal)}}}, 201)
	require.Equal(t, "DRAFT", p.State)
	read := call(2, "GET", "/plans/"+p.ID.String(), "", nil, 200)
	require.Equal(t, p.ContentHash, read.ContentHash)
	p = call(2, "POST", "/plans/"+p.ID.String()+"/decisions", "submit-plan", PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "SUBMIT"}, 201)
	require.Equal(t, "SUBMITTED", p.State)
	p = call(3, "POST", "/plans/"+p.ID.String()+"/decisions", "confirm-plan", PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "CONFIRM"}, 201)
	require.Equal(t, "CONFIRMED", p.State)
	p = call(2, "PATCH", "/plans/"+p.ID.String(), "edit-confirmed-plan", map[string]any{"version": p.Version, "expected_content_hash": p.ContentHash, "content": PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(goal), linkedItem(goal)}}}, 200)
	require.Equal(t, "SUBMITTED", p.State)
	r := call(2, "POST", "/reports", "create-report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}, 201)
	require.NotNil(t, r.Facts)
	content := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Pending work", Retrospective: "Use evidence", NextPlan: []PlanningPlanItem{linkedItem(goal)}, DeclaredConfidence: "YELLOW"}
	r = call(2, "PATCH", "/reports/"+r.ID.String(), "edit-report", map[string]any{"version": r.Version, "content": content}, 200)
	call(2, "GET", "/reports/"+r.ID.String()+"/reconciliation", "", nil, 200)
	r = call(2, "POST", "/reports/"+r.ID.String()+"/decisions", "submit-report", PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "SUBMIT"}, 201)
	r = call(3, "POST", "/reports/"+r.ID.String()+"/decisions", "return-report", PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "RETURN", Reason: "Clarify remaining work"}, 201)
	require.Equal(t, "RETURNED", r.State)
	r = call(2, "POST", "/reports/"+r.ID.String()+"/decisions", "resubmit-report", PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "SUBMIT"}, 201)
	r = call(3, "POST", "/reports/"+r.ID.String()+"/decisions", "confirm-report", PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "CONFIRM"}, 201)
	require.True(t, r.CanReopen, "the confirmer need not reload to correct a confirmed report")
	reopen := PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "REOPEN", Reason: "Correct the retrospective"}
	call(2, "POST", "/reports/"+r.ID.String()+"/decisions", "self-reopen-report", reopen, 403)
	call(4, "POST", "/reports/"+r.ID.String()+"/decisions", "unrelated-reopen-report", reopen, 403)
	call(7, "POST", "/reports/"+r.ID.String()+"/decisions", "agent-reopen-report", reopen, 403)
	r = call(3, "POST", "/reports/"+r.ID.String()+"/decisions", "reopen-report", reopen, 201)
	require.Equal(t, "RETURNED", r.State)
	require.Nil(t, r.ConfirmedBy)
	replayed := call(3, "POST", "/reports/"+r.ID.String()+"/decisions", "reopen-report", reopen, 201)
	require.Equal(t, r.Version, replayed.Version)
	policy := h.m.entityPolicy
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	call(3, "POST", "/reports/"+r.ID.String()+"/decisions", "reopen-report", reopen, 403)
	h.m.entityPolicy = policy
	for _, kind := range []string{"goals", "plans", "reports"} {
		for _, sort := range []string{"id", "-id"} {
			path := "/tenants/1/" + kind + "?project_id=3&limit=1&sort=" + sort
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(claims(t, 2)))
			require.Equal(t, 200, rec.Code, rec.Body.String())
		}
	}
}

func TestGoalHTTPFencesReplayAndCurrentMutationAuthority(t *testing.T) {
	h := setup(t)
	member := goalTree(t, h)
	var goal PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &goal))
	draft := create(t, h, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &goal})
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	change := func(body PlanningGoalChange, key string, status int) PlanningRecord {
		t.Helper()
		b, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/goals/"+draft.ID.String()+"/changes", strings.NewReader(string(b))).WithContext(claims(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		var envelope struct {
			Data PlanningRecord `json:"data"`
		}
		if status < 300 {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		}
		return envelope.Data
	}
	goal.Title = "HTTP edited objective"
	draft = change(PlanningGoalChange{Version: draft.Version, ExpectedContentHash: draft.ContentHash, Action: "EDIT", Content: &goal}, "goal-edit", 201)
	change(PlanningGoalChange{Version: draft.Version - 1, ExpectedContentHash: draft.ContentHash, Action: "ACTIVATE"}, "stale-goal", 409)
	activation := PlanningGoalChange{Version: draft.Version, ExpectedContentHash: draft.ContentHash, Action: "ACTIVATE"}
	active := change(activation, "activate-goal", 201)
	require.Equal(t, "ACTIVE", active.State)
	replayed := change(activation, "activate-goal", 201)
	require.Equal(t, active.Version, replayed.Version)
	old := h.m.entityPolicy
	h.m.entityPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "goal_member_manage" {
			return authz.DataConstraint{}, nil
		}
		return old(ctx, tx, tenant, project, permission, actor)
	}
	change(activation, "activate-goal", 403)
	for _, path := range []string{"/goals/999", "/plans/999", "/reports/999", "/reports/999/reconciliation"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tenants/1"+path, nil).WithContext(claims(t, 2)))
		require.Equal(t, 404, rec.Code, rec.Body.String())
	}
}

func TestReportScopeValidationBindsCursorToAggregateScope(t *testing.T) {
	for _, in := range []PlanningListInput{
		{Scope: "UNKNOWN", Limit: 1}, {Scope: "PROJECT", Limit: 1}, {Scope: "TEAM", ProjectID: "3", Limit: 1}, {Scope: "TEAM", PrincipalID: "2", Limit: 1},
	} {
		_, _, _, err := listScope(&in, 1, 2, "report")
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, _, _, err := listScope(&PlanningListInput{Scope: "TEAM", Limit: 1}, 1, 2, "goal")
	require.ErrorIs(t, err, ErrInvalid)
	project, subject, scope, err := listScope(&PlanningListInput{Scope: "PROJECT", ProjectID: "3", Limit: 1}, 1, 2, "report")
	require.NoError(t, err)
	require.Equal(t, guid.ID(3), project)
	require.Zero(t, subject)
	_, _, memberScope, err := listScope(&PlanningListInput{ProjectID: "3", Limit: 1}, 1, 2, "report")
	require.NoError(t, err)
	require.NotEqual(t, scope, memberScope)
}

func TestHTTPPaginationRechecksAuthorityAndBindsCursors(t *testing.T) {
	h := setup(t)
	goalTree(t, h)
	goalTree(t, h)
	ctx := claims(t, 2)
	first, e := h.m.listHTTP(ctx, "goal", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Len(t, first.Body.Data.Items, 1)
	cursor := first.Body.Data.NextCursor
	require.NotEmpty(t, cursor)
	next, e := h.m.listHTTP(ctx, "goal", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: "id", Cursor: cursor})
	require.NoError(t, e)
	require.False(t, next.Body.Data.HasMore)
	require.NotEqual(t, first.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	_, e = h.m.listHTTP(ctx, "goal", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: "-id", Cursor: cursor})
	require.Error(t, e)
	_, e = h.m.listHTTP(claims(t, 3), "goal", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: "id", Cursor: cursor})
	require.Error(t, e)
	denied := h.m.policy
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = h.m.listHTTP(ctx, "goal", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1})
	require.Error(t, e)
	h.m.policy = denied
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	for _, p := range []string{"/tenants/{tenant_id}/goals", "/tenants/{tenant_id}/plans", "/tenants/{tenant_id}/reports"} {
		require.Contains(t, api.OpenAPI().Paths, p)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/tenants/1/goals?limit=1", 200}, {"/tenants/2/goals?limit=1", 403}, {"/tenants/1/goals?limit=0", 422}, {"/tenants/1/goals?cursor=invalid", 422}, {"/tenants/1/reports?project_id=invalid", 422}} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx))
		require.Equal(t, tc.status, rec.Code, rec.Body.String())
	}
}
func TestHTTPCommandsReplayWithoutDuplicatingWritesAndRejectRevokedAuthority(t *testing.T) {
	h := setup(t)
	ctx := claims(t, 2)
	body := PlanningWriteInput{PrincipalID: 2, PeriodKind: "QUARTER", PeriodStart: "2026-10-01", Goal: &PlanningGoalInput{Level: "TEAM", Title: "Recorded objective", KeyResults: []PlanningKeyResult{{Key: "x", Title: "Result", Source: "MANUAL", Target: 2}}}}
	fn := func(tx *gorm.DB) (PlanningRecord, error) { return h.m.createOn(ctx, tx, 1, "goal", body) }
	first, e := h.m.command(ctx, 1, "goal-key", "goal.create", "/goals", body, fn)
	require.NoError(t, e)
	second, e := h.m.command(ctx, 1, "goal-key", "goal.create", "/goals", body, func(*gorm.DB) (PlanningRecord, error) {
		t.Fatal("replay must not execute creation twice")
		return PlanningRecord{}, nil
	})
	require.NoError(t, e)
	require.Equal(t, "true", second.Replayed)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	body.Goal.Title = "Different objective"
	_, e = h.m.command(ctx, 1, "goal-key", "goal.create", "/goals", body, fn)
	require.Error(t, e)
	body.Goal.Title = "Recorded objective"
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = h.m.command(ctx, 1, "goal-key", "goal.create", "/goals", body, fn)
	require.Error(t, e)
	var count int64
	require.NoError(t, h.db.Table("p_planning_goals").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestHTTPErrorsAndMalformedScopesNeverExposeInternals(t *testing.T) {
	h := setup(t)
	ctx := claims(t, 2)
	encoded, err := json.Marshal(planningError(ctx, ErrImprovementInvalid))
	require.NoError(t, err)
	var improvementError struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(encoded, &improvementError))
	require.Equal(t, 180461, improvementError.Code)
	for _, tc := range []struct {
		e      error
		status int
	}{{ErrInvalid, 422}, {idempotency.ErrInvalidKey, 422}, {ErrForbidden, 403}, {ErrNotFound, 404}, {ErrConflict, 409}, {idempotency.ErrKeyReused, 409}, {idempotency.ErrInProgress, 409}, {ErrUnavailable, 503}, {errors.New("secret database DSN"), 500}} {
		var status huma.StatusError
		e := planningError(ctx, tc.e)
		require.ErrorAs(t, e, &status)
		require.Equal(t, tc.status, status.GetStatus())
		require.NotContains(t, e.Error(), "secret database")
	}
	for _, in := range []PlanningListInput{{PlanningTenantInput: PlanningTenantInput{TenantID: "bad"}, Limit: 1}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "bad", Limit: 1}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, PrincipalID: "bad", Limit: 1}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 101}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: "unknown"}} {
		_, e := h.m.listHTTP(ctx, "report", &in)
		require.Error(t, e)
	}
	for _, p := range []PlanningRecordPath{{PlanningTenantInput: PlanningTenantInput{TenantID: "bad"}, RecordID: "1"}, {PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, RecordID: "bad"}} {
		_, _, e := recordIDs(&p)
		require.ErrorIs(t, e, ErrInvalid)
	}
	_, e := cursorPosition("%%%", "scope")
	require.ErrorIs(t, e, ErrInvalid)
	_, e = cursorPosition("c2NvcGU", "scope")
	require.ErrorIs(t, e, ErrInvalid)
	_, e = h.m.command(ctx, 2, "key", "goal.create", "/goals", nil, nil)
	require.Error(t, e)
	_, e = h.m.command(ctx, 1, strings.Repeat("x", 65), "goal.create", "/goals", nil, nil)
	require.Error(t, e)
	h.m.replay = nil
	_, e = h.m.command(ctx, 1, "key", "goal.create", "/goals", nil, nil)
	require.Error(t, e)
	h.m.runtime = nil
	_, e = h.m.database()
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestPlanningScopedListPaginationAndStorageFailures(t *testing.T) {
	for _, kind := range []string{"goal", "plan", "report"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			table := h.m.names.Goals.String()
			if kind == "plan" {
				table = h.m.names.Plans.String()
				create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
				create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-21", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
			}
			if kind == "report" {
				table = h.m.names.Reports.String()
				reportDraft(t, h)
				create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-21"})
			}
			if kind == "goal" {
				var body PlanningGoalInput
				require.NoError(t, json.Unmarshal(g.Content, &body))
				create(t, h, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &body})
			}
			for _, sort := range []string{"id", "-id"} {
				in := &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "3", PrincipalID: "2", Limit: 1, Sort: sort}
				first, e := h.m.listHTTP(claims(t, 2), kind, in)
				require.NoError(t, e)
				require.Len(t, first.Body.Data.Items, 1)
				require.NotEmpty(t, first.Body.Data.NextCursor)
				in.Cursor = first.Body.Data.NextCursor
				second, e := h.m.listHTTP(claims(t, 2), kind, in)
				require.NoError(t, e)
				require.Len(t, second.Body.Data.Items, 1)
				require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
			}
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("planning_list_read", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(ErrUnavailable)
				}
			}))
			out, e := h.m.listHTTP(claims(t, 2), kind, &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "3", PrincipalID: "2", Limit: 10})
			require.Error(t, e)
			require.Nil(t, out)
			require.NoError(t, h.db.Callback().Query().Remove("planning_list_read"))
			if kind != "plan" {
				column := "content"
				if kind == "report" {
					column = "facts"
				}
				require.NoError(t, h.db.Table(table).Where("project_id=? AND principal_id=?", 3, 2).Update(column, "[").Error)
				out, e = h.m.listHTTP(claims(t, 2), kind, &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "3", PrincipalID: "2", Limit: 10})
				require.Error(t, e)
				require.Nil(t, out)
			}
		})
	}
}
