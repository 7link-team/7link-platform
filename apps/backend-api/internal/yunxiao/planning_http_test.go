package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestPlanningAssignmentHTTPParsesEmbeddedPathParameters(t *testing.T) {
	_, api := humatest.New(t)
	var parsed *assignmentInput
	huma.Register(api, huma.Operation{OperationID: "planning-assignment-input", Method: "PUT", Path: "/tenants/{tenant_id}/projects/{project_id}/work-items/{work_item_id}/planning"}, func(_ context.Context, in *assignmentInput) (*struct{ Body string }, error) {
		parsed = in
		return &struct{ Body string }{Body: "ok"}, nil
	})
	body := `{"iteration_id":"456","version_id":null,"milestone_id":null}`
	response := api.Put("/tenants/1/projects/99/work-items/77/planning", "Idempotency-Key: planning", "If-Match: \"0\"", strings.NewReader(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotNil(t, parsed)
	require.Equal(t, "1", parsed.TenantID)
	require.Equal(t, "99", parsed.ProjectID)
	require.Equal(t, "77", parsed.WorkItemID)
	require.Equal(t, "planning", parsed.IdempotencyKey)
	require.Equal(t, "\"0\"", parsed.IfMatch)
	require.NotNil(t, parsed.Body.IterationID)
	require.EqualValues(t, 456, *parsed.Body.IterationID)
	for _, path := range []string{"/tenants/invalid/projects/99/work-items/77/planning", "/tenants/1/projects/0/work-items/77/planning", "/tenants/1/projects/99/work-items/invalid/planning"} {
		parsed = nil
		response := api.Put(path, "Idempotency-Key: planning", strings.NewReader(body))
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Nil(t, parsed, "invalid parameters must not reach the handler")
	}
}

func TestPlanningAssignmentHTTPReplayRechecksCurrentAuthority(t *testing.T) {
	m, _, ctx := workHarness(t)
	m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
		return delivery.PlanningWorkIdentity{State: "IN_PROGRESS", Type: "TASK"}, nil
	}
	in := &assignmentInput{PlanningAssignmentPath: PlanningAssignmentPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorkItemID: "77"}, WriteHeaders: WriteHeaders{IdempotencyKey: "planning-assign", IfMatch: "\"0\""}}
	first, err := m.putPlanningAssignmentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 1, first.Body.Data.Version)
	replay, err := m.putPlanningAssignmentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
		return delivery.PlanningWorkIdentity{}, ErrForbidden
	}
	_, err = m.putPlanningAssignmentHTTP(ctx, in)
	require.Error(t, err)
}

func TestPlanningPublicCatalogPaginationLifecycleAndCurrentAuthority(t *testing.T) {
	m, db, ctx := workHarness(t)
	a := testAuth{}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), a, a, a)
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "work_item", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
	}, nil))
	_, api := humatest.New(t)
	require.NoError(t, m.registerPlanningHTTP(api))
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	for _, kind := range []string{"iterations", "versions", "milestones"} {
		t.Run(kind, func(t *testing.T) {
			body := PlanningEntityInput{Name: "First", StartsAt: &start, EndsAt: &end, DueAt: &end}
			var first PlanningEntity
			for i := 0; i < 2; i++ {
				body.Name = fmt.Sprintf("Entity %d", i)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					v, e := m.savePlanningEntityOn(ctx, tx, 1, 99, 0, kind, 0, body)
					if i == 0 {
						first = v
					}
					return e
				}))
			}
			base := "/tenants/1/projects/99/" + kind
			response := api.Get(base + "?limit=1")
			require.Equal(t, 200, response.Code, response.Body.String())
			var page struct {
				Data Page[PlanningEntity] `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
			require.True(t, page.Data.HasMore)
			require.Len(t, page.Data.Items, 1)
			require.Equal(t, first.ID, page.Data.Items[0].ID)
			response = api.Get(base + "?limit=1&cursor=" + page.Data.NextCursor)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
			require.Len(t, page.Data.Items, 1)
			require.NotEqual(t, first.ID, page.Data.Items[0].ID)
			require.Equal(t, 200, api.Get(base+"?sort=id&cursor="+first.ID.String()).Code)
			if kind == "iterations" {
				require.Equal(t, 404, api.Get(base+"?cursor=999999").Code)
			}
			if kind == "milestones" {
				require.Equal(t, 422, api.Get(base+"?state=ACTIVE").Code)
				require.Equal(t, 200, api.Get(base+"?state=ACHIEVED").Code)
			} else {
				require.Equal(t, 200, api.Get(base+"?state="+first.State).Code)
			}
			body.Name = "New entity"
			encoded, e := json.Marshal(body)
			require.NoError(t, e)
			response = api.Post(base, "Idempotency-Key: create-"+kind, strings.NewReader(string(encoded)))
			require.Equal(t, 201, response.Code, response.Body.String())
			body.Name = "Renamed entity"
			encoded, e = json.Marshal(body)
			require.NoError(t, e)
			response = api.Patch(base+"/"+first.ID.String(), "Idempotency-Key: update-"+kind, "If-Match: \"1\"", strings.NewReader(string(encoded)))
			require.Equal(t, 200, response.Code, response.Body.String())
			if kind != "milestones" {
				state := "ACTIVE"
				if kind == "versions" {
					state = "RELEASED"
				}
				response = api.Put(base+"/"+first.ID.String()+"/state", "Idempotency-Key: state-"+kind, "If-Match: \"2\"", strings.NewReader(fmt.Sprintf(`{"state":%q,"reason":"ready"}`, state)))
				require.Equal(t, 200, response.Code, response.Body.String())
			}
			scope := m.projects
			m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
			require.Equal(t, 403, api.Get(base).Code)
			m.projects = scope
		})
	}
}

func TestPlanningAssignmentReadReturnsOnlyCurrentVisiblePersistedAssignment(t *testing.T) {
	m, db, ctx := workHarness(t)
	path := PlanningAssignmentPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorkItemID: "77"}
	_, e := m.getPlanningAssignmentHTTP(ctx, &path)
	require.Error(t, e)
	m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
		return delivery.PlanningWorkIdentity{Type: "TASK"}, nil
	}
	empty, e := m.getPlanningAssignmentHTTP(ctx, &path)
	require.NoError(t, e)
	require.Zero(t, empty.Body.Data.Version)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{})
		return e
	}))
	found, e := m.getPlanningAssignmentHTTP(ctx, &path)
	require.NoError(t, e)
	require.Equal(t, 1, found.Body.Data.Version)
	m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
		return delivery.PlanningWorkIdentity{}, ErrForbidden
	}
	_, e = m.getPlanningAssignmentHTTP(ctx, &path)
	require.Error(t, e)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	_, e = m.getPlanningAssignmentHTTP(ctx, &path)
	require.Error(t, e)
	path.WorkItemID = "0"
	_, e = m.getPlanningAssignmentHTTP(ctx, &path)
	require.Error(t, e)
	_, e = m.putPlanningAssignmentHTTP(ctx, &assignmentInput{PlanningAssignmentPath: path})
	require.Error(t, e)
}
