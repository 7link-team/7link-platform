package webhook

import (
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryReadAndWriteRefusesWithoutADatasource covers the branch each
// repository method opens when the datasource is gone. They have to report it
// rather than panic on a nil handle, because a datasource can be replaced
// underneath a running worker.
func TestEveryReadAndWriteRefusesWithoutADatasource(t *testing.T) {
	repo := NewRepository(appenv.NewRuntime(appenv.NewIdentity("webhook-test", "1")))
	now := time.Now().UTC()
	row := subscriptionRow{ID: guid.ID(1), TenantID: guid.ID(1)}

	assert.ErrorIs(t, repo.insertSubscription(t.Context(), &row), errRepositoryUnavailable)
	assert.ErrorIs(t, repo.updateSubscription(t.Context(), &row), errRepositoryUnavailable)
	assert.ErrorIs(t, repo.deleteSubscription(t.Context(), guid.ID(1), guid.ID(1)), errRepositoryUnavailable)
	_, err := repo.markDelivered(t.Context(), guid.ID(1), 1, now)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.markAttemptFailed(t.Context(), guid.ID(1), 1, DeliveryFailed, "why", now, now)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.resetStaleDeliveries(t.Context(), now, now)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repo.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)

	_, err = repo.getSubscription(t.Context(), guid.ID(1), guid.ID(1))
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = repo.listSubscriptions(t.Context(), guid.ID(1), 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = repo.listDeliveries(t.Context(), guid.ID(1), guid.ID(1), 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.activeSubscriptions(t.Context(), 0, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.dueDeliveries(t.Context(), now, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.claimDelivery(t.Context(), guid.ID(1), 0, now)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.subscriptionOwner(t.Context(), guid.ID(1))
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.nameTaken(t.Context(), guid.ID(1), "n", 0)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

func TestUpdatingAndDeletingAnAbsentSubscriptionSaysSo(t *testing.T) {
	env := newWebhookEnvironment(t)

	_, err := env.service.UpdateSubscription(t.Context(), guid.ID(1), guid.ID(999999), SubscriptionInput{
		Name: "n", URL: "https://receiver.example",
	})
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)

	assert.ErrorIs(t, env.service.DeleteSubscription(t.Context(), guid.ID(1), guid.ID(999999)), ErrSubscriptionNotFound)

	_, err = env.service.GetSubscription(t.Context(), guid.ID(1), guid.ID(999999))
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)

	_, _, err = env.service.ListDeliveries(t.Context(), guid.ID(1), guid.ID(999999), 0, 10)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound, "deliveries are read through their subscription")
}

func TestAnotherTenantCannotReachASubscriptionByItsIdentifier(t *testing.T) {
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))

	_, err := env.service.GetSubscription(t.Context(), guid.ID(2), item.ID)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound, "the tenant is part of the lookup, not a filter applied after it")
	assert.ErrorIs(t, env.service.DeleteSubscription(t.Context(), guid.ID(2), item.ID), ErrSubscriptionNotFound)
}

func TestDeliveryStopsWhenTheEventIsNoLongerRetained(t *testing.T) {
	// Audit's retention window closes behind the chain. There is nothing left to
	// deliver and no reason to keep trying, so the delivery is abandoned with a
	// reason rather than retried until its attempts run out.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	env.stream.mu.Lock()
	env.stream.events[guid.ID(1)] = nil
	env.stream.mu.Unlock()

	require.NoError(t, env.service.DeliverDue(t.Context()))
	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	assert.Equal(t, DeliveryFailed, owed[0].Status)
	assert.Contains(t, owed[0].LastError, "no longer retained")
	assert.Zero(t, target.count(), "nothing was sent for an event that is gone")
}

func TestFanOutReportsAChainItCannotRead(t *testing.T) {
	// A chain that cannot be read is not an empty chain: treating it as one would
	// advance the cursor past events nobody has seen.
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))
	env.stream.mu.Lock()
	env.stream.err = assertAnError
	env.stream.mu.Unlock()

	require.Error(t, env.service.FanOut(t.Context()))
	stored, err := env.service.GetSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, stored.AfterSequence, "the cursor does not move past what was never read")
}

var assertAnError = assert.AnError

func TestModuleRegisterHTTPMountsRoutesWithoutAService(t *testing.T) {
	// A module whose storage is not ready still mounts its routes, so each one can
	// answer unavailable instead of the process refusing to start.
	_, api := humatest.New(t)
	instance := NewModule(newWebhookEnvironment(t).runtime, Config{})
	instance.registrar = authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	)

	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Equal(t, http.StatusServiceUnavailable,
		humatest.Wrap(t, api).Get("/tenants/1/webhook-subscriptions").Code)
}

func TestNewModulePanicsWithoutARuntime(t *testing.T) {
	// A module with no runtime could never resolve a datasource, and finding that
	// out at the first delivery would be far from the cause.
	assert.Panics(t, func() { NewModule(nil, Config{}) })
}

func TestDeliveryReportsAReceiverItCannotReach(t *testing.T) {
	// A receiver that has gone away is a transport failure, not a status, and the
	// reason has to reach the attempt history or an operator sees only "failed".
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	target.server.Close()

	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	require.NoError(t, env.service.DeliverDue(t.Context()))

	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	assert.Equal(t, DeliveryPending, owed[0].Status, "a receiver that is down is worth retrying")
	assert.NotEmpty(t, owed[0].LastError)
}

func TestRenamingOntoAnotherSubscriptionsNameIsRefused(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	first := env.subscribe(target)
	second := env.subscribe(target)

	_, err := env.service.UpdateSubscription(t.Context(), guid.ID(1), second.ID, SubscriptionInput{
		Name: first.Name, URL: target.server.URL,
	})
	assert.ErrorIs(t, err, ErrSubscriptionNameTaken)

	// Keeping its own name is not a conflict with itself.
	kept, err := env.service.UpdateSubscription(t.Context(), guid.ID(1), second.ID, SubscriptionInput{
		Name: second.Name, URL: target.server.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, second.Name, kept.Name)
}

func TestSubscriptionListingIsPagedAndCounted(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	for index := 0; index < 3; index++ {
		env.subscribe(target)
	}

	page, total, err := env.service.ListSubscriptions(t.Context(), guid.ID(1), 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total, "the total counts what exists, not what this page holds")
	require.Len(t, page, 1)
	assert.Equal(t, "receiver-1", page[0].Name, "the order is by name, so a page is stable")
}

func TestEnqueueYieldsWhenAnotherReplicaMovedTheCursor(t *testing.T) {
	// Two replicas run the same pass. The one that loses the race must not fail:
	// the work it was about to record is the work the winner recorded.
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))
	env.stream.append(guid.ID(1), "principal_created")

	stored, err := env.service.repo.getSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)

	require.NoError(t, env.service.FanOut(t.Context()))

	// Replay the pass against the cursor as it was before the winner moved it.
	require.NoError(t, env.service.repo.enqueue(t.Context(), stored, nil, 1, webhookTestNow))
	assert.Len(t, env.deliveries(item.ID), 1, "the loser adds nothing and reports no error")
}

func TestUpdatingASubscriptionRotatesItsSecret(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)

	before, err := env.service.repo.getSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)

	rotated, err := env.service.UpdateSubscription(t.Context(), guid.ID(1), item.ID, SubscriptionInput{
		Name: item.Name, URL: target.server.URL, Secret: "a-new-secret",
	})
	require.NoError(t, err)
	assert.True(t, rotated.SecretSet)

	after, err := env.service.repo.getSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)
	assert.NotEqual(t, before.SecretCiphertext, after.SecretCiphertext, "a rotation replaces what was stored")

	opened, err := env.service.openSecret(after)
	require.NoError(t, err)
	assert.Equal(t, "a-new-secret", string(opened))
}

func TestDeliveryStopsWhenTheSigningSecretCannotBeOpened(t *testing.T) {
	// A secret sealed with a key the deployment no longer holds cannot be used to
	// sign, and sending unsigned would be worse than not sending: a receiver that
	// verifies would reject it, and one that does not would trust it.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	env.service.keys = nil
	require.NoError(t, env.service.DeliverDue(t.Context()))

	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	assert.NotEmpty(t, owed[0].LastError)
	assert.Zero(t, target.count(), "nothing is sent that could not be signed")
}

func TestListDeliveriesIsPagedWithinItsSubscription(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	for index := 0; index < 3; index++ {
		env.stream.append(guid.ID(1), "principal_created")
	}
	require.NoError(t, env.service.FanOut(t.Context()))

	page, total, err := env.service.ListDeliveries(t.Context(), guid.ID(1), item.ID, 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, page, 1)
	assert.EqualValues(t, 2, page[0].EventSequence, "newest first, so the second page holds the middle event")
}

func TestOutcomeWritesAreFencedOnTheClaimThatMadeThem(t *testing.T) {
	// A claim is identified by the attempt count it left on the row. Once the
	// lease is taken back -- and again once it is claimed anew -- an outcome from
	// the older claim matches nothing and must change nothing.
	env := newWebhookEnvironment(t)
	item := env.subscribe(newReceiver(t))
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	id := env.deliveries(item.ID)[0].ID
	repo := env.service.repo
	now := webhookTestNow

	claimed, err := repo.claimDelivery(t.Context(), id, 0, now)
	require.NoError(t, err)
	require.True(t, claimed)

	// Only claims older than the cutoff are taken back.
	reclaimed, err := repo.resetStaleDeliveries(t.Context(), now, now)
	require.NoError(t, err)
	assert.Zero(t, reclaimed, "a claim made at the cutoff is still in force")
	reclaimed, err = repo.resetStaleDeliveries(t.Context(), now.Add(time.Second), now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, reclaimed)

	stale := func() {
		t.Helper()
		recorded, err := repo.markDelivered(t.Context(), id, 1, now)
		require.NoError(t, err)
		assert.False(t, recorded)
		recorded, err = repo.markAttemptFailed(t.Context(), id, 1, DeliveryFailed, "late", now, now)
		require.NoError(t, err)
		assert.False(t, recorded)
	}
	stale()
	assert.Equal(t, DeliveryPending, env.deliveries(item.ID)[0].Status, "a reclaimed row waits for its next claim")

	claimed, err = repo.claimDelivery(t.Context(), id, 1, now)
	require.NoError(t, err)
	require.True(t, claimed)
	stale()
	current := env.deliveries(item.ID)[0]
	assert.Equal(t, DeliveryDelivering, current.Status)
	assert.Empty(t, current.LastError)

	recorded, err := repo.markAttemptFailed(t.Context(), id, 2, DeliveryPending, "refused", now, now)
	require.NoError(t, err)
	assert.True(t, recorded, "the claim in force records its outcome")
	assert.Equal(t, "refused", env.deliveries(item.ID)[0].LastError)

	claimed, err = repo.claimDelivery(t.Context(), id, 2, now)
	require.NoError(t, err)
	require.True(t, claimed)
	recorded, err = repo.markDelivered(t.Context(), id, 3, now)
	require.NoError(t, err)
	assert.True(t, recorded)
	assert.Equal(t, DeliveryDelivered, env.deliveries(item.ID)[0].Status)
}

func TestActiveSubscriptionsResumeAfterTheGivenKey(t *testing.T) {
	// The later tenant's subscription is created first, so its id is the
	// smallest: only an ordering by tenant before id puts it last.
	env := newWebhookEnvironment(t)
	other, err := env.service.CreateSubscription(t.Context(), guid.ID(2), SubscriptionInput{Name: "other", URL: "http://127.0.0.1:1"})
	require.NoError(t, err)
	first := env.subscribe(newReceiver(t))
	second := env.subscribe(newReceiver(t))

	rows, err := env.service.repo.activeSubscriptions(t.Context(), first.TenantID, first.ID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, second.ID, rows[0].ID, "the same tenant continues past the key")
	assert.Equal(t, other.ID, rows[1].ID, "then later tenants, whatever their ids")

	rows, err = env.service.repo.activeSubscriptions(t.Context(), other.TenantID, other.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "nothing follows the last key")
}
