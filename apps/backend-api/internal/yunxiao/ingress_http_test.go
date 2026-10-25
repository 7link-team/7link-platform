package yunxiao

import (
	"bytes"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestIngressHTTPPublicSignatureBodyAndReplayContract(t *testing.T) {
	m, _, ctx, c, _ := ingressFixture(t)
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), testAuth{}, testAuth{}, testAuth{})
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{{Resource: "integration", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage external integrations"}}, nil))
	_, api := humatest.New(t)
	m.registerIngressHTTP(api)
	for _, path := range []string{"/integration-webhooks/{connection_id}/codeup", "/integration-webhooks/{connection_id}/flow"} {
		op := api.OpenAPI().Paths[path].Post
		require.True(t, authz.IsPublicOperation(op))
		require.Equal(t, int64(1<<20), op.MaxBodyBytes)
		require.NotNil(t, op.Responses["200"])
		require.Equal(t, op.Responses["202"].Content, op.Responses["200"].Content)
	}
	path := "/integration-webhooks/" + c.ID.String() + "/codeup"
	bad := api.Post(path, "X-Delivery-Key: mr-1", "X-Codeup-Token: wrong", bytes.NewReader(codeupTestBody()))
	require.Equal(t, http.StatusUnauthorized, bad.Code)
	require.Contains(t, bad.Body.String(), "190021")
	first := api.Post(path, "X-Delivery-Key: mr-1", "X-Codeup-Token: "+connectionBody().WebhookSecret, bytes.NewReader(codeupTestBody()))
	require.Equal(t, http.StatusAccepted, first.Code, first.Body.String())
	replay := api.Post(path, "X-Delivery-Key: mr-1", "X-Codeup-Token: "+connectionBody().WebhookSecret, bytes.NewReader(codeupTestBody()))
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Contains(t, replay.Body.String(), `"replayed":true`)
	missing := api.Post(path, "X-Codeup-Token: "+connectionBody().WebhookSecret, bytes.NewReader(codeupTestBody()))
	require.Equal(t, 422, missing.Code)
	_, e := m.codeupIngressHTTP(ctx, &ingressHTTPInput{ConnectionID: "0"})
	var status huma.StatusError
	require.ErrorAs(t, e, &status)
	require.Equal(t, 422, status.GetStatus())
}

func TestIngressObservationHTTPRejectsInvalidIdentityAndUnavailableStorage(t *testing.T) {
	m, _, ctx := configHarness(t)
	for _, in := range []*ingressObservationListInput{
		{MappingPath: MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "0"}}}},
		{MappingPath: MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "0"}}},
		{MappingPath: MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "1"}, MappingID: "0"}},
	} {
		_, e := m.ingressObservationsHTTP(ctx, in)
		require.Error(t, e)
	}
	valid := &ingressObservationListInput{MappingPath: MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "1"}, MappingID: "1"}, Limit: 1}
	_, e := (&Module{}).ingressObservationsHTTP(ctx, valid)
	require.Error(t, e)
	_, e = m.codeupIngressHTTP(ctx, &ingressHTTPInput{ConnectionID: "0"})
	require.Error(t, e)
}
