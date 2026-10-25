package core

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entityDiscoveryEvaluator struct {
	httpAuthorizationEvaluator
	constraints map[string]authz.DataConstraint
	calls       []string
	err         error
}

func (e *entityDiscoveryEvaluator) Constraint(_ context.Context, tenant guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
	e.calls = append(e.calls, permission)
	if tenant != 1 || subject != 11 {
		return authz.DataConstraint{}, nil
	}
	return e.constraints[permission], e.err
}

func TestMemberEntitiesBatchKeepsIndependentScopesAndFailsClosed(t *testing.T) {
	h := newHTTPIAMHarness(t, true)
	a, err := h.module.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "company", Name: "First"})
	require.NoError(t, err)
	b, err := h.module.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "company", Name: "Second"})
	require.NoError(t, err)
	evaluator := &entityDiscoveryEvaluator{constraints: map[string]authz.DataConstraint{
		"entity_view": {AllowAll: true, DeniedIDs: []string{b.ID.String()}},
		"store_view":  {ResourceIDs: []string{b.ID.String()}},
	}}
	h.module.service.checker = evaluator
	path := "/tenants/1/principals/11/entities"
	response := h.api.Get(path + "?permissions=entity_view,store_view,entity_view")
	require.Equal(t, 200, response.Code, response.Body.String())
	items := decodeEnvelope[[]APIEntity](t, response).Data
	require.Len(t, items, 2)
	assert.ElementsMatch(t, []guid.ID{a.ID, b.ID}, []guid.ID{items[0].ID, items[1].ID})
	assert.Equal(t, []string{"entity_view", "store_view"}, evaluator.calls)
	for _, query := range []string{"?permissions=entity_view,missing", "?permissions=entity_view,role_view", "?permission=entity_view&permissions=store_view", "?permissions=", ""} {
		r := h.api.Get(path + query)
		assert.NotEqual(t, 200, r.Code, query)
	}
	assert.Equal(t, 422, h.api.Get(path+"?permissions="+strings.Repeat("entity_view,", 32)+"entity_view").Code)
	evaluator.err = ErrUnavailable
	r := h.api.Get(path + "?permissions=entity_view,store_view")
	assert.Equal(t, 503, r.Code, r.Body.String())
	evaluator.err = nil
	single := h.api.Get(path + "?permission=entity_view")
	require.Equal(t, 200, single.Code)
	assert.Equal(t, a.ID, decodeEnvelope[[]APIEntity](t, single).Data[0].ID)
	assert.Len(t, decodeEnvelope[[]APIEntity](t, single).Data, 1)
	assert.NotEqual(t, 200, h.api.Get("/tenants/1/principals/12/entities?permissions=entity_view").Code)
	assert.NotEqual(t, 200, h.api.Get("/tenants/2/principals/11/entities?permissions=entity_view").Code)
}
