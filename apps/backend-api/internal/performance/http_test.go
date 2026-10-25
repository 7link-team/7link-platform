package performance

import (
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"testing"
)

func status(t *testing.T, e error, want int) {
	t.Helper()
	var h huma.StatusError
	require.ErrorAs(t, e, &h)
	require.Equal(t, want, h.GetStatus())
}
func TestPerformanceHTTPDeclarationsPaginationAndFailureContracts(t *testing.T) {
	m, _, ctx := harness(t)
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "performance", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage owner assessments"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(ctx, api))
	path := api.OpenAPI().Paths["/tenants/{tenant_id}/performance-schemes"]
	require.NotNil(t, path)
	require.Contains(t, path.Post.Responses, "201")
	a, e := m.createHTTP(ctx, &createInput{IdempotencyKey: "a", Body: schemeBody{Definition: definition(), Reason: "A"}})
	require.NoError(t, e)
	_, e = m.createHTTP(ctx, &createInput{IdempotencyKey: "b", Body: schemeBody{Definition: definition(), Reason: "B"}})
	require.NoError(t, e)
	first, e := m.listHTTP(ctx, &listInput{Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	second, e := m.listHTTP(ctx, &listInput{Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.NotEqual(t, first.Body.Data.Items[0].SchemeID, second.Body.Data.Items[0].SchemeID)
	_, e = m.reviseHTTP(ctx, &reviseInput{SchemeID: a.Body.Data.SchemeID.String(), IdempotencyKey: "r", Body: schemeBody{BaseVersion: 1, Definition: definition(), Reason: "revision"}})
	require.NoError(t, e)
	history, e := m.historyHTTP(ctx, &historyInput{SchemeID: a.Body.Data.SchemeID.String(), Limit: 1})
	require.NoError(t, e)
	require.True(t, history.Body.Data.HasMore)
	require.Equal(t, "1", history.Body.Data.NextCursor)
	tail, e := m.historyHTTP(ctx, &historyInput{SchemeID: a.Body.Data.SchemeID.String(), Limit: 1, Cursor: 1})
	require.NoError(t, e)
	require.False(t, tail.Body.Data.HasMore)
	require.Equal(t, 2, tail.Body.Data.Items[0].Version)
	for _, in := range []*listInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Cursor: "bad"}} {
		_, e = m.listHTTP(ctx, in)
		status(t, e, 422)
	}
	for _, in := range []*historyInput{{SchemeID: "bad", Limit: 1}, {SchemeID: "1", Limit: 0}, {SchemeID: "1", Limit: 1, Cursor: -1}} {
		_, e = m.historyHTTP(ctx, in)
		status(t, e, 422)
	}
	_, e = m.getHTTP(ctx, &getInput{SchemeID: "bad"})
	status(t, e, 422)
	_, e = m.getHTTP(ctx, &getInput{SchemeID: a.Body.Data.SchemeID.String(), Version: 99})
	status(t, e, 404)
	_, e = m.reviseHTTP(ctx, &reviseInput{SchemeID: "bad"})
	status(t, e, 422)
	_, e = m.reviseHTTP(ctx, &reviseInput{SchemeID: "1"})
	status(t, e, 422)
	_, e = m.createHTTP(ctx, &createInput{IdempotencyKey: "invalid", Body: schemeBody{Definition: definition(), Reason: " "}})
	status(t, e, 422)
	m.registrar = nil
	require.ErrorIs(t, m.RegisterHTTP(ctx, api), ErrUnavailable)
	var missing *Module
	_, e = missing.getHTTP(context.Background(), &getInput{SchemeID: "1"})
	status(t, e, 503)
	status(t, failure(ctx, errors.New("private database text")), 500)
	status(t, failure(ctx, ErrUnavailable), 503)
}

func TestPerformanceHTTPRealSchemaValidationAndDomainRoundTrip(t *testing.T) {
	m, _, ctx := harness(t)
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "performance", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage owner assessment schemes"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	api.UseMiddleware(func(request huma.Context, next func(huma.Context)) {
		next(huma.WithContext(request, authn.WithClaims(request.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})))
	})
	require.NoError(t, m.RegisterHTTP(ctx, api))
	response := api.Post("/tenants/1/performance-schemes", "Idempotency-Key: http-round-trip", schemeBody{Definition: definition(), Reason: "HTTP fixture"})
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Delivery review")
	response = api.Get("/tenants/1/performance-schemes?limit=1")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "\"has_more\":false")
	response = api.Post("/tenants/1/performance-schemes", "Idempotency-Key: invalid-http", schemeBody{Definition: SchemeDefinition{}, Reason: "invalid"})
	require.Equal(t, 422, response.Code)
}
