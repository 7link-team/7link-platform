package webhook

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	webhooki18n "7link.com/platform/internal/iam/webhook/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookErrorMapsEachRefusalToItsOwnStatus(t *testing.T) {
	// Each of these means something different to a caller, so collapsing any two
	// of them would send an operator looking in the wrong place.
	for _, probe := range []struct {
		err    error
		status int
		key    string
	}{
		{ErrSubscriptionNotFound, http.StatusNotFound, webhooki18n.SubscriptionMissing},
		{ErrSubscriptionNameTaken, http.StatusConflict, webhooki18n.SubscriptionExists},
		{ErrInvalidSubscription, http.StatusUnprocessableEntity, webhooki18n.InvalidRequest},
		{ErrUnavailable, http.StatusServiceUnavailable, webhooki18n.Unavailable},
		{errRepositoryUnavailable, http.StatusServiceUnavailable, webhooki18n.Unavailable},
		{errors.New("something else"), http.StatusInternalServerError, webhooki18n.Unavailable},
	} {
		mapped := webhookError(probe.err)
		require.Error(t, mapped)
		status, ok := mapped.(interface{ GetStatus() int })
		require.True(t, ok, "%v must map to a status error", probe.err)
		assert.Equal(t, probe.status, status.GetStatus(), "for %v", probe.err)
		assert.Contains(t, mapped.Error(), probe.key, "for %v", probe.err)
	}

	// A status error that already arrived from a lower layer passes through, so a
	// precise answer is not flattened into a generic one.
	already := huma.Error400BadRequest("webhook.already_decided")
	assert.Equal(t, already, webhookError(already))
}

func TestRegisterHTTPDeclaresEveryOperationWithItsGuard(t *testing.T) {
	// The operation set and the permission each one requires are declared
	// together, so a route added without a guard is visible here.
	_, api := humatest.New(t)
	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	)
	env := newWebhookEnvironment(t)

	RegisterHTTP(api, env.service, registrar)
	require.NoError(t, authz.ValidateOperations(api, registrar.Registry()))

	operations := map[string]string{}
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post,
			http.MethodPut: item.Put, http.MethodDelete: item.Delete,
		} {
			if operation != nil && strings.HasPrefix(operation.OperationID, "webhook-") {
				operations[operation.OperationID] = method + " " + path
			}
		}
	}
	for id, expected := range map[string]string{
		"webhook-list-subscriptions":  "GET /tenants/{tenant_id}/webhook-subscriptions",
		"webhook-create-subscription": "POST /tenants/{tenant_id}/webhook-subscriptions",
		"webhook-update-subscription": "PUT /webhook-subscriptions/{subscription_id}",
		"webhook-delete-subscription": "DELETE /webhook-subscriptions/{subscription_id}",
		"webhook-list-deliveries":     "GET /webhook-subscriptions/{subscription_id}/deliveries",
	} {
		assert.Equal(t, expected, operations[id], "operation %s", id)
	}
	assert.Len(t, operations, 5, "no operation is registered that this test does not name")
}

func TestResolveOwnerKeepsSubscriptionsInsideTheirTenant(t *testing.T) {
	// The authorization layer asks who owns a subscription before admitting a
	// request that names one by id. Answering the wrong tenant here would be a
	// cross-tenant read.
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))

	owner, found, err := env.service.resolveOwner(t.Context(), item.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, guid.ID(1), owner.TenantID)

	_, found, err = env.service.resolveOwner(t.Context(), guid.ID(999999))
	require.NoError(t, err, "an unknown id is not found rather than an error")
	assert.False(t, found)

	var absent *Service
	_, _, err = absent.resolveOwner(t.Context(), item.ID)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestSubscriptionInputFromCarriesTheBodyThrough(t *testing.T) {
	input := subscriptionInputFrom(subscriptionBody{
		Name: "n", URL: "https://r", Secret: "s", EventTypes: []string{"a"}, Status: "disabled",
	})
	assert.Equal(t, SubscriptionInput{
		Name: "n", URL: "https://r", Secret: "s", EventTypes: []string{"a"}, Status: "disabled",
	}, input)
}

// The three checkers the registrar needs. This test only asks whether operations
// and guards were declared together, so each one answers permissively.
type webhookTokenVerifier struct{}

func (webhookTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{PrincipalID: guid.ID(9)}, nil
}

type webhookPermissionChecker struct{}

func (webhookPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (webhookPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type webhookMembershipChecker struct{}

func (webhookMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
