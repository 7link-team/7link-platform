package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strconv"
	"testing"
)

func TestEvalHTTPActualRequestValidationAndFrozenRunRoundtrip(t *testing.T) {
	m, _, _, v, draft := evalFixture(t)
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "template", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read"}, {Resource: "template", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) {
		next(huma.WithContext(c, authn.WithClaims(c.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})))
	})
	m.registerEvalHTTP(api)
	response := api.Post("/tenants/1/eval-cases", "Idempotency-Key: http-case", draft)
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"binding_id"`)
	response = api.Post("/tenants/1/eval-cases", draft)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = api.Get("/tenants/1/eval-cases?limit=1")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"has_more":false`)
	start := EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured"}}
	response = api.Post("/tenants/1/eval-runs", "Idempotency-Key: http-run", start)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"state":"UNAVAILABLE"`)
	require.Contains(t, response.Body.String(), `"agreement_ppm":null`)
	response = api.Patch("/tenants/1/eval-cases/999", "Idempotency-Key: no-match", map[string]any{"active": false})
	require.Equal(t, 422, response.Code, response.Body.String())
}

func TestEvalHTTPReplayCurrentAuthorityPaginationAndRequiredHeaders(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	input := &evalCaseCreateInput{IdempotencyKey: "create-case", Body: draft}
	first, err := m.evalCreateHTTP(ctx, input)
	require.NoError(t, err)
	again, err := m.evalCreateHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, again.Body.Data)
	input.IdempotencyKey = "create-second"
	input.Body.Title = "Second known sample"
	_, err = m.evalCreateHTTP(ctx, input)
	require.NoError(t, err)
	page, err := m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, err := m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, err)
	require.Len(t, next.Body.Data.Items, 1)
	require.False(t, next.Body.Data.HasMore)
	start := &evalStartInput{IdempotencyKey: "start-eval", Body: EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured"}}}
	run, err := m.evalStartHTTP(ctx, start)
	require.NoError(t, err)
	replay, err := m.evalStartHTTP(ctx, start)
	require.NoError(t, err)
	require.Equal(t, run.Body.Data, replay.Body.Data)
	savedScope := m.scope
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	_, err = m.evalStartHTTP(ctx, start)
	assertStatus(t, err, 403)
	m.scope = savedScope
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.evalStartHTTP(ctx, start)
	assertStatus(t, err, 403)
	_, err = m.evalCreateHTTP(ctx, &evalCaseCreateInput{IdempotencyKey: "create-case", Body: draft})
	assertStatus(t, err, 403)
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "template", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read"}, {Resource: "template", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	m.registerEvalHTTP(api)
	require.Contains(t, api.OpenAPI().Paths, "/tenants/{tenant_id}/eval-cases")
	require.NotContains(t, api.OpenAPI().Paths, "/tenants/{tenant_id}/tenants/{tenant_id}/eval-cases")
	patch := api.OpenAPI().Paths["/tenants/{tenant_id}/eval-cases/{case_id}"].Patch
	headers := map[string]bool{}
	for _, p := range patch.Parameters {
		if p.In == "header" {
			headers[p.Name] = p.Required
		}
	}
	require.True(t, headers["If-Match"])
	require.True(t, headers["Idempotency-Key"])
	var count int64
	require.NoError(t, db.Table(m.names.EvalRuns.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestEvalHTTPFilteredCasesRunPagingAndCAS(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	first := addEvalFixture(t, m, db, ctx, draft)
	draft.Title = "Second"
	addEvalFixture(t, m, db, ctx, draft)
	active := false
	title := "Archived sample"
	patch := &evalCasePatchInput{ID: first.ID.String(), IfMatch: "1", IdempotencyKey: "archive-case", Body: EvalCasePatch{Title: &title, Active: &active}}
	changed, e := m.evalPatchHTTP(ctx, patch)
	require.NoError(t, e)
	require.False(t, changed.Body.Data.Active)
	require.Equal(t, title, changed.Body.Data.Title)
	replay, e := m.evalPatchHTTP(ctx, patch)
	require.NoError(t, e)
	require.Equal(t, changed.Body.Data, replay.Body.Data)
	patch.IdempotencyKey = "stale-case"
	_, e = m.evalPatchHTTP(ctx, patch)
	assertStatus(t, e, 409)
	page, e := m.evalListHTTP(ctx, &evalListInput{TemplateKey: v.Key, Origin: "MANUAL", Active: "false", Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, first.ID, page.Body.Data.Items[0].ID)
	var runID guid.ID
	for i := 0; i < 2; i++ {
		out, e := m.evalStartHTTP(ctx, &evalStartInput{IdempotencyKey: "paged-run-" + strconv.Itoa(i), Body: EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured"}}})
		require.NoError(t, e)
		runID = out.Body.Data.ID
	}
	runs, e := m.evalRunsHTTP(ctx, &evalListInput{TemplateKey: v.Key, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, runs.Body.Data.Items, 1)
	require.True(t, runs.Body.Data.HasMore)
	next, e := m.evalRunsHTTP(ctx, &evalListInput{TemplateKey: v.Key, Limit: 1, Sort: "id", Cursor: runs.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.False(t, next.Body.Data.HasMore)
	require.Equal(t, runID, next.Body.Data.Items[0].ID)
	got, e := m.evalGetHTTP(ctx, &evalGetInput{ID: runID.String()})
	require.NoError(t, e)
	require.Equal(t, next.Body.Data.Items[0], got.Body.Data)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	hidden, e := m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Empty(t, hidden.Body.Data.Items)
	hiddenRuns, e := m.evalRunsHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Empty(t, hiddenRuns.Body.Data.Items)
	_, e = m.evalGetHTTP(ctx, &evalGetInput{ID: runID.String()})
	assertStatus(t, e, 403)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error {
		return errors.New("scope unavailable")
	}}
	_, e = m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	_, e = m.evalRunsHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
}
func TestEvalHTTPInvalidPagingAndStorage(t *testing.T) {
	m, db, ctx, _, draft := evalFixture(t)
	addEvalFixture(t, m, db, ctx, draft)
	for _, in := range []evalListInput{{Limit: 0, Sort: "id"}, {Limit: 101, Sort: "id"}, {Limit: 1, Sort: "bad"}, {Limit: 1, Sort: "id", Cursor: "bad"}, {Limit: 1, Sort: "id", Active: "bad"}} {
		_, e := m.evalListHTTP(ctx, &in)
		assertStatus(t, e, 422)
	}
	_, e := m.evalRunsHTTP(ctx, &evalListInput{Limit: 0, Sort: "id"})
	assertStatus(t, e, 422)
	_, e = m.evalPatchHTTP(ctx, &evalCasePatchInput{ID: "1", IfMatch: "bad"})
	assertStatus(t, e, 422)
	_, e = m.evalGetHTTP(ctx, &evalGetInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.evalGetHTTP(ctx, &evalGetInput{ID: "999"})
	assertStatus(t, e, 404)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("eval-storage-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.EvalCases.String() || tx.Statement.Table == m.names.EvalRuns.String() {
			tx.AddError(errors.New("storage unavailable"))
		}
	}))
	_, e = m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	_, e = m.evalRunsHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	require.NoError(t, db.Callback().Query().Remove("eval-storage-failure"))
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("tenant_id=?", 1).Update("expected", "invalid").Error)
	_, e = m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 409)
}

func TestEvalHTTPScansHiddenBatchesAndRechecksTemplateVisibility(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	created := addEvalFixture(t, m, db, ctx, draft)
	var source model.EvalCase
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("id=?", created.ID).Take(&source).Error)
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("id=?", created.ID).Update("project_id", 98).Error)
	rows := make([]*model.EvalCase, 100)
	for i := range rows {
		snapshot := source
		id, e := m.ids.Next()
		require.NoError(t, e)
		snapshot.ID = guid.ID(id)
		if i < 99 {
			snapshot.ProjectID = 98
		}
		rows[i] = &snapshot
	}
	require.NoError(t, db.Table(m.names.EvalCases.String()).Create(rows).Error)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(_ context.Context, _ *gorm.DB, _, p guid.ID, _ string) error {
		if p == 98 {
			return ErrForbidden
		}
		return nil
	}}
	page, e := m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, rows[99].ID, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
	}
	hidden, e := m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Empty(t, hidden.Body.Data.Items)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.evalListHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 403)
	_, e = m.evalRunsHTTP(ctx, &evalListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 403)
	require.NotZero(t, v.ID)
}
