package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"testing"
)

func TestWorkflowPreviewHTTPRechecksAuthorityAndReturnsNoWriteProjection(t *testing.T) {
	m, db, ctx := workHarness(t)
	input := &workflowPreviewInput{WorkflowPath: WorkflowPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorkItemType: "TASK"}}
	input.Body.Definition = workflowBody()
	out, err := m.previewWorkflowHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "PROJECT", out.Body.Data.After.Source)
	require.Equal(t, "NOT_EVALUATED", out.Body.Data.RuntimeImpact)
	var count int64
	require.NoError(t, db.Table("test_effects").Count(&count).Error)
	require.Zero(t, count)
	input.Body.Version = 1
	_, err = m.previewWorkflowHTTP(ctx, input)
	require.Error(t, err)
	input.Body.Version = 0
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	_, err = m.previewWorkflowHTTP(ctx, input)
	require.Error(t, err)
	input.ProjectID = "0"
	_, err = m.previewWorkflowHTTP(ctx, input)
	require.Error(t, err)
	input.ProjectID = "99"
	_, err = (&Module{}).previewWorkflowHTTP(ctx, input)
	require.Error(t, err)
}

func TestWorklogHTTPIdempotencyRechecksAuthorityAndSummary(t *testing.T) {
	m, _, ctx := workHarness(t)
	in := &worklogCreateInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WriteHeaders: WriteHeaders{IdempotencyKey: "worklog-create-1"}, Body: workInput()}
	first, e := m.createWorklogHTTP(ctx, in)
	require.NoError(t, e)
	again, e := m.createWorklogHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	summary, e := m.summaryWorklogsHTTP(ctx, &in.WorkProjectPath)
	require.NoError(t, e)
	require.Zero(t, summary.Body.Data.ApprovedMinutes)
	page, e := m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: in.WorkProjectPath, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	_, e = m.createWorklogHTTP(ctx, in)
	require.Error(t, e)
}

func TestWorkflowHTTPRoundTripPreconditionsAndDeletionVersion(t *testing.T) {
	m, _, ctx := workHarness(t)
	path := WorkflowPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorkItemType: "TASK"}
	in := &workflowSaveInput{WorkflowPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "workflow-create"}, Body: workflowBody()}
	created, e := m.saveWorkflowHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 1, created.Body.Data.Version)
	replay, e := m.saveWorkflowHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	view, e := m.getWorkflowHTTP(ctx, &path)
	require.NoError(t, e)
	require.Equal(t, "PROJECT", view.Body.Data.Source)
	for _, state := range workflowBody().States {
		require.Contains(t, view.Body.Data.States, state)
	}
	for _, cell := range workflowBody().Transitions {
		require.Contains(t, view.Body.Data.Transitions, cell)
	}
	require.Equal(t, created.Body.Data, view.Body.Data, "writes and reads expose the same effective matrix")
	in.IdempotencyKey = "workflow-bad-version"
	in.IfMatch = `"99"`
	_, e = m.saveWorkflowHTTP(ctx, in)
	require.Error(t, e)
	deleted, e := m.deleteWorkflowHTTP(ctx, &workflowDeleteInput{WorkflowPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "workflow-delete", IfMatch: `"1"`}})
	require.NoError(t, e)
	require.Equal(t, 2, deleted.Body.Data.Version)
	view, e = m.getWorkflowHTTP(ctx, &path)
	require.NoError(t, e)
	require.Equal(t, "SYSTEM", view.Body.Data.Source)
	require.Equal(t, defaultWorkflow("TASK").WorkflowInput, view.Body.Data.WorkflowInput, "deletion restores system defaults and retains the tombstone version")
	require.Equal(t, 2, view.Body.Data.Version)
}

func TestWorklogDimensionsMatchListAndApprovedSummary(t *testing.T) {
	m, db, ctx := workHarness(t)
	path := WorkProjectPath{TenantInput: TenantInput{"1"}, ProjectID: "99"}
	m.effortWorkOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
		if tenant != 1 || project != 99 || work != 77 {
			return ErrNotFound
		}
		return nil
	}
	work := guid.ID(77)
	bad := guid.ID(78)
	in := workInput()
	in.WorkItemID = &bad
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, in); return e }), ErrNotFound)
	in.WorkItemID = &work
	for _, day := range []string{"2026-10-19", "2026-10-20", "2026-10-21"} {
		in.WorkDate = day
		var row WorklogView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, in); return e }))
		require.Equal(t, &work, row.WorkItemID)
		// Explicit approved projection fixture: period aggregation is the subject,
		// approval authority is tested by the separate command lifecycle tests.
		require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("state", "APPROVED").Error)
	}
	filter := WorklogPeriod{WorkItemID: "77", From: "2026-10-20", Until: "2026-10-21"}
	page, e := m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path, WorklogPeriod: filter, Limit: 25})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, "2026-10-20", page.Body.Data.Items[0].WorkDate)
	summary, e := m.summaryWorklogsPeriodHTTP(ctx, &worklogSummaryInput{WorkProjectPath: path, WorklogPeriod: filter})
	require.NoError(t, e)
	require.EqualValues(t, 60, summary.Body.Data.ApprovedMinutes)
	require.EqualValues(t, 60, summary.Body.Data.ByWorkItem["77"])
	require.EqualValues(t, 60, summary.Body.Data.ByDate["2026-10-20"])
	filter.WorkItemID = "78"
	_, e = m.summaryWorklogsPeriodHTTP(ctx, &worklogSummaryInput{WorkProjectPath: path, WorklogPeriod: filter})
	require.Error(t, e, "hidden task scope must not return an apparently authorized summary")
	m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	empty, e := m.summaryWorklogsPeriodHTTP(ctx, &worklogSummaryInput{WorkProjectPath: path, WorklogPeriod: filter})
	require.NoError(t, e)
	require.Zero(t, empty.Body.Data.ApprovedMinutes)
	for _, p := range []WorklogPeriod{{From: "2026-02-30"}, {WorkItemID: "0"}, {From: "2026-10-21", Until: "2026-10-20"}} {
		_, e = m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path, WorklogPeriod: p})
		require.Error(t, e)
	}
}

// Tenant-wide denial must not veto a current grant on the selected project.
type projectReadHTTPAuth struct{ testAuth }

func (projectReadHTTPAuth) Authenticate(ctx context.Context, authorization, cookie string) (*authn.Claims, error) {
	scope, ok := authn.PersonalTokenScopeFromContext(ctx)
	if !ok || scope.Permission != "work_item_read" || authorization == "Bearer limited" {
		return nil, authn.ErrInvalidToken
	}
	return testAuth{}.Authenticate(ctx, authorization, cookie)
}

func (projectReadHTTPAuth) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}
func (projectReadHTTPAuth) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}
func TestProjectReadsUseCurrentProjectAuthorityAtHTTPBoundary(t *testing.T) {
	m, _, ctx := workHarness(t)
	a := projectReadHTTPAuth{}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), a, a, a)
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "worklog", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
	}, nil))
	_, api := humatest.New(t)
	require.NoError(t, m.registerWorkHTTP(api))
	for _, suffix := range []string{"worklogs", "worklog-summary", "workflow-configs/TASK", "workflow-configs/TASK?team_default=true"} {
		path := "/tenants/1/projects/99/" + suffix
		require.Equal(t, http.StatusOK, api.Get(path).Code, path)
		require.Equal(t, http.StatusUnauthorized, api.Get(path, "Authorization: Bearer limited").Code, path)
		require.Equal(t, http.StatusForbidden, api.Get("/tenants/1/projects/100/"+suffix).Code, suffix)
		require.Equal(t, http.StatusForbidden, api.Get("/tenants/2/projects/99/"+suffix).Code, suffix)
		scope := m.projects
		m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
		require.Equal(t, http.StatusForbidden, api.Get(path).Code, "revoked "+suffix)
		m.projects = scope
	}
	_ = ctx
}

func TestWorklogPaginationReturnsStableScopedRowsAndRejectsUnavailableReads(t *testing.T) {
	m, db, ctx := workHarness(t)
	path := WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput()); return e }))
	}
	page, e := m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path, Limit: 1})
	require.NoError(t, e)
	require.True(t, page.Body.Data.HasMore)
	require.Len(t, page.Body.Data.Items, 1)
	next, e := m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path, Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.NotEqual(t, page.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	require.False(t, next.Body.Data.HasMore)
	_, e = m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path, Cursor: "bad"})
	require.Error(t, e)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("worklog_read", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Worklogs.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	defer db.Callback().Query().Remove("worklog_read")
	_, e = m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path})
	require.Error(t, e)
	_, e = m.summaryWorklogsHTTP(ctx, &path)
	require.Error(t, e)
	_, e = (&Module{}).listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: path})
	require.Error(t, e)
	_, e = (&Module{}).summaryWorklogsHTTP(ctx, &path)
	require.Error(t, e)
	_, e = (&Module{}).getWorkflowHTTP(ctx, &WorkflowPath{WorkProjectPath: path, WorkItemType: "TASK"})
	require.Error(t, e)
	bad := path
	bad.ProjectID = "0"
	_, e = m.listWorklogsHTTP(ctx, &worklogListInput{WorkProjectPath: bad})
	require.Error(t, e)
	_, e = m.summaryWorklogsHTTP(ctx, &bad)
	require.Error(t, e)
	_, e = m.getWorkflowHTTP(ctx, &WorkflowPath{WorkProjectPath: bad})
	require.Error(t, e)
	_, e = m.saveWorkflowHTTP(ctx, &workflowSaveInput{WorkflowPath: WorkflowPath{WorkProjectPath: bad}})
	require.Error(t, e)
	_, e = m.saveWorkflowHTTP(ctx, &workflowSaveInput{WorkflowPath: WorkflowPath{WorkProjectPath: path}, WriteHeaders: WriteHeaders{IfMatch: "bad"}})
	require.Error(t, e)
}
