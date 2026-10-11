package webhook

import (
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// brokenEnvironment is a service whose tables have been dropped. Every statement
// then fails for a real reason, which is how the error return of each repository
// method gets exercised without a mock standing in for storage.
func brokenEnvironment(t *testing.T) *webhookEnvironment {
	t.Helper()
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	require.NotEmpty(t, env.deliveries(item.ID))

	require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_webhook_deliveries").Error)
	require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_webhook_subscriptions").Error)
	return env
}

func TestEveryOperationReportsAStorageFaultRatherThanSucceeding(t *testing.T) {
	env := brokenEnvironment(t)

	_, err := env.service.CreateSubscription(t.Context(), guid.ID(1), SubscriptionInput{
		Name: "n", URL: "https://receiver.example",
	})
	require.Error(t, err)

	_, err = env.service.UpdateSubscription(t.Context(), guid.ID(1), guid.ID(1), SubscriptionInput{
		Name: "n", URL: "https://receiver.example",
	})
	require.Error(t, err)

	require.Error(t, env.service.DeleteSubscription(t.Context(), guid.ID(1), guid.ID(1)))

	_, _, err = env.service.ListSubscriptions(t.Context(), guid.ID(1), 0, 10)
	require.Error(t, err)

	_, _, err = env.service.ListDeliveries(t.Context(), guid.ID(1), guid.ID(1), 0, 10)
	require.Error(t, err)

	_, err = env.service.GetSubscription(t.Context(), guid.ID(1), guid.ID(1))
	require.Error(t, err)

	// A fan-out pass that cannot read its own subscriptions must report it rather
	// than conclude there is nothing to do.
	require.Error(t, env.service.FanOut(t.Context()))
	require.Error(t, env.service.DeliverDue(t.Context()))

	_, _, err = env.service.resolveOwner(t.Context(), guid.ID(1))
	require.Error(t, err)
}

func TestRESTReportsAStorageFaultOnEveryRoute(t *testing.T) {
	env := brokenEnvironment(t)
	api := newWebhookAPI(t, env)

	// Each handler has its own error exit; a route that swallowed the fault would
	// answer 200 with an empty body here.
	assert.GreaterOrEqual(t, api.Get("/tenants/1/webhook-subscriptions").Code, 500)
	assert.GreaterOrEqual(t, api.Post("/tenants/1/webhook-subscriptions",
		map[string]any{"name": "n", "url": "https://receiver.example"}).Code, 500)
}

func TestDeliveryLeaseIsNotTakenTwice(t *testing.T) {
	// Two replicas claim the same row. The attempt count is in the predicate, so
	// exactly one wins and the loser does nothing rather than sending twice.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	now := time.Now().UTC()

	first, err := env.service.repo.claimDelivery(t.Context(), owed[0].ID, 0, now)
	require.NoError(t, err)
	assert.True(t, first)

	second, err := env.service.repo.claimDelivery(t.Context(), owed[0].ID, 0, now)
	require.NoError(t, err)
	assert.False(t, second, "the attempt count the loser expected is no longer there")
}

func TestRESTRefusesAnUnparseableBody(t *testing.T) {
	env := newWebhookEnvironment(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, env.service, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	))
	wrapped := humatest.Wrap(t, api)

	// A body missing what the contract requires is refused by the contract, not
	// by the service.
	assert.Equal(t, http.StatusUnprocessableEntity,
		wrapped.Post("/tenants/1/webhook-subscriptions", map[string]any{"name": "no-url"}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity,
		wrapped.Post("/tenants/1/webhook-subscriptions", map[string]any{"url": "https://r"}).Code)
}

func TestRESTReportsAStorageFaultOnTheOwnedRoutes(t *testing.T) {
	// The owned routes resolve ownership before the handler runs, so a storage
	// fault there is refused before any work is attempted. Reaching them at all
	// requires an id that was real when ownership was resolved, which is why this
	// drops the tables after the listing proves them present.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	api := newWebhookAPI(t, env)
	require.Equal(t, http.StatusOK, api.Get("/tenants/1/webhook-subscriptions").Code)

	require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_webhook_deliveries").Error)

	deliveries := api.Get("/webhook-subscriptions/" + item.ID.String() + "/deliveries")
	assert.GreaterOrEqual(t, deliveries.Code, 500, deliveries.Body.String())
}

func TestFanOutReportsASubscriptionItCannotEnqueueFor(t *testing.T) {
	// The deliveries table is gone but the subscriptions table is not, so the pass
	// reads the chain, decides there is work, and then cannot record it. Advancing
	// the cursor anyway would lose the event.
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_webhook_deliveries").Error)

	require.Error(t, env.service.FanOut(t.Context()))

	stored, err := env.service.repo.getSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, stored.AfterSequence, "the cursor stays put when the work could not be recorded")
}
