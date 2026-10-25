package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestDeliveryConfirmationHTTPActualBindingReplayAndCurrentAuthority(t *testing.T) {
	m, _, ctx, source := deliveryConfirmationHarness(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "delivery", Verb: "compose", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: ctx}
	m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	m.entityOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, true, nil
	}
	_, api := humatest.New(t)
	m.registerDeliveryConfirmationHTTP(api)
	path := "/entities/99/project/delivery-packages/81/confirmation-requests"
	require.Equal(t, 422, api.Post(path, strings.NewReader(`{"source_version":1}`)).Code)
	first := api.Post(path, "Idempotency-Key: confirm-once", strings.NewReader(`{"source_version":1}`))
	require.Equal(t, 201, first.Code, first.Body.String())
	var receipt struct {
		Data DeliveryConfirmationFact `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &receipt))
	require.Positive(t, receipt.Data.ID)
	replay := api.Post(path, "Idempotency-Key: confirm-once", strings.NewReader(`{"source_version":1}`))
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	conflict := api.Post(path, "Idempotency-Key: confirm-once", strings.NewReader(`{"source_version":2}`))
	require.Equal(t, 422, conflict.Code, conflict.Body.String())
	source.Version = 2
	stale := api.Post(path, "Idempotency-Key: stale-source", strings.NewReader(`{"source_version":1}`))
	require.Equal(t, 409, stale.Code, stale.Body.String())
	withdrawPath := "/entities/99/project/delivery-confirmations/" + receipt.Data.ID.String() + "/withdrawal"
	withdrawn := api.Put(withdrawPath, "Idempotency-Key: withdraw-once")
	require.Equal(t, 200, withdrawn.Code, withdrawn.Body.String())
	withdrawnReplay := api.Put(withdrawPath, "Idempotency-Key: withdraw-once")
	require.Equal(t, 200, withdrawnReplay.Code, withdrawnReplay.Body.String())
	require.Equal(t, "true", withdrawnReplay.Header().Get("Idempotent-Replayed"))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := api.Post(path, "Idempotency-Key: confirm-once", strings.NewReader(`{"source_version":1}`))
	require.Equal(t, 403, denied.Code, denied.Body.String())
	op := api.OpenAPI().Paths[deliveryConfirmationPath].Post
	require.Equal(t, "delivery_compose", op.Extensions[authz.GuardExtensionKey])
	require.NotEmpty(t, op.Security)
}
