package webhook

import (
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWebhookAPI mounts the real routes against a real service, so these exercise
// the handlers rather than the service behind them.
func newWebhookAPI(t *testing.T, env *webhookEnvironment) humatest.TestAPI {
	t.Helper()
	_, api := humatest.New(t)
	RegisterHTTP(api, env.service, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	))
	testAPI, _ := humatest.New(t)
	_ = testAPI
	return humatest.Wrap(t, api)
}

func TestSubscriptionRESTRoundTrip(t *testing.T) {
	env := newWebhookEnvironment(t)
	api := newWebhookAPI(t, env)
	target := newReceiver(t)

	created := api.Post("/tenants/1/webhook-subscriptions", map[string]any{
		"name": "orders", "url": target.server.URL, "secret": "shh",
		"event_types": []string{"role_granted", "principal_created"},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	assert.Contains(t, created.Body.String(), `"secret_set":true`)
	assert.NotContains(t, created.Body.String(), "shh", "a secret is never read back")

	listed := api.Get("/tenants/1/webhook-subscriptions")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), "orders")

	items, _, err := env.service.ListSubscriptions(t.Context(), guid.ID(1), 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	id := items[0].ID.String()

	replaced := api.Put("/webhook-subscriptions/"+id, map[string]any{
		"name": "orders", "url": target.server.URL, "status": "disabled",
	})
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Contains(t, replaced.Body.String(), `"status":"disabled"`)
	assert.Contains(t, replaced.Body.String(), `"secret_set":true`, "an omitted secret keeps the stored one")

	deliveries := api.Get("/webhook-subscriptions/" + id + "/deliveries")
	require.Equal(t, http.StatusOK, deliveries.Code, deliveries.Body.String())

	deleted := api.Delete("/webhook-subscriptions/" + id)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.Contains(t, deleted.Body.String(), `"deleted":true`)

	remaining, _, err := env.service.ListSubscriptions(t.Context(), guid.ID(1), 0, 10)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestSubscriptionRESTRefusesWhatCannotBeStored(t *testing.T) {
	env := newWebhookEnvironment(t)
	api := newWebhookAPI(t, env)
	target := newReceiver(t)

	// A receiver the outbound policy refuses is rejected while the operator is
	// still looking at the form.
	refused := api.Post("/tenants/1/webhook-subscriptions", map[string]any{
		"name": "bad", "url": "ftp://example.com/hook",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())

	first := api.Post("/tenants/1/webhook-subscriptions", map[string]any{"name": "dup", "url": target.server.URL})
	require.Equal(t, http.StatusCreated, first.Code)
	again := api.Post("/tenants/1/webhook-subscriptions", map[string]any{"name": "dup", "url": target.server.URL})
	assert.Equal(t, http.StatusConflict, again.Code, "a tenant cannot have two subscriptions with one name")

	missing := api.Get("/webhook-subscriptions/999999/deliveries")
	assert.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

func TestSubscriptionRESTAnswersUnavailableWithoutAService(t *testing.T) {
	// Routes are registered even when storage is not ready, so each one has to
	// say so rather than the process refusing to start.
	_, api := humatest.New(t)
	RegisterHTTP(api, nil, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	))
	wrapped := humatest.Wrap(t, api)

	assert.Equal(t, http.StatusServiceUnavailable, wrapped.Get("/tenants/1/webhook-subscriptions").Code)
	assert.Equal(t, http.StatusServiceUnavailable,
		wrapped.Post("/tenants/1/webhook-subscriptions", map[string]any{"name": "n", "url": "https://r"}).Code)
}

func TestRESTRejectsAnIdentifierThatIsNotOne(t *testing.T) {
	env := newWebhookEnvironment(t)
	api := newWebhookAPI(t, env)

	// Measured, not assumed, because the two kinds of path are checked in
	// opposite orders: a resource identifier is validated before anything is
	// authorized, while a tenant path is authorized first because the tenant is
	// the boundary rather than a resource inside one.
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/webhook-subscriptions/abc/deliveries").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/webhook-subscriptions/0/deliveries").Code,
		"zero is not an identifier this platform issues")
	assert.Equal(t, http.StatusForbidden, api.Get("/tenants/abc/webhook-subscriptions").Code)
}

func TestRESTReportsAnUpdateAndDeleteOfAnAbsentSubscription(t *testing.T) {
	env := newWebhookEnvironment(t)
	api := newWebhookAPI(t, env)

	absent := api.Put("/webhook-subscriptions/999999", map[string]any{
		"name": "n", "url": "https://receiver.example",
	})
	assert.Equal(t, http.StatusNotFound, absent.Code, absent.Body.String())
	assert.Equal(t, http.StatusNotFound, api.Delete("/webhook-subscriptions/999999").Code)
}

func TestRESTAnswersUnavailableOnEveryWriteWithoutAService(t *testing.T) {
	_, api := humatest.New(t)
	RegisterHTTP(api, nil, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	))
	wrapped := humatest.Wrap(t, api)

	assert.Equal(t, http.StatusServiceUnavailable,
		wrapped.Put("/webhook-subscriptions/1", map[string]any{"name": "n", "url": "https://r"}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, wrapped.Delete("/webhook-subscriptions/1").Code)
	assert.Equal(t, http.StatusServiceUnavailable, wrapped.Get("/webhook-subscriptions/1/deliveries").Code)
}
