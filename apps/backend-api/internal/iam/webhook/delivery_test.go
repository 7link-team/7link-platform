package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignBindsTheTimestampToTheBody(t *testing.T) {
	secret := []byte("the-signing-secret")
	body := []byte(`{"event":{"id":"1"}}`)

	signature := sign(secret, 1700000000, body)

	// Verify the way a receiver does, so a change to the signed content shows up
	// here rather than at a customer's endpoint.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("1700000000"))
	mac.Write([]byte{'.'})
	mac.Write(body)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), signature)

	// The timestamp is inside the signed content, so replaying a body under a
	// different timestamp does not verify.
	assert.NotEqual(t, signature, sign(secret, 1700000001, body))
	assert.NotEqual(t, signature, sign(secret, 1700000000, append(body, ' ')))
	assert.NotEqual(t, signature, sign([]byte("another-secret"), 1700000000, body))
}

func TestSignIsStableAcrossCalls(t *testing.T) {
	// A receiver that stores a signature to detect duplicates needs the same
	// input to produce the same output.
	body := []byte("payload")
	first := sign([]byte("s"), 42, body)
	assert.Equal(t, first, sign([]byte("s"), 42, body))
	assert.Len(t, first, sha256.Size*2, "a hex SHA-256 digest")
}

func TestMatchesEventTypeTreatsAnEmptySelectionAsEverything(t *testing.T) {
	// A tenant that wants everything would otherwise have to keep a list in step
	// with every event type this platform ever adds.
	assert.True(t, matchesEventType("", "principal_created"))
	assert.True(t, matchesEventType("   ", "anything"))

	assert.True(t, matchesEventType("principal_created federation_login", "federation_login"))
	assert.False(t, matchesEventType("principal_created", "federation_login"))
	assert.False(t, matchesEventType("principal_created", "principal_created_extra"),
		"matching is by whole type, not by prefix")
}

func TestNormalizeEventTypesSortsAndDeduplicates(t *testing.T) {
	// Two subscriptions asking for the same thing should store the same value,
	// otherwise the stored list reads as meaningful ordering that it is not.
	types, err := normalizeEventTypes([]string{"role_granted", "principal_created", "role_granted", "  "})
	require.NoError(t, err)
	assert.Equal(t, "principal_created role_granted", types)

	empty, err := normalizeEventTypes(nil)
	require.NoError(t, err)
	assert.Empty(t, empty, "no selection means every type")
}

func TestNormalizeEventTypesRefusesWhatCouldNotBeMatched(t *testing.T) {
	// A type containing a space could never match: the stored list is
	// space-separated, so it would be read back as two types.
	_, err := normalizeEventTypes([]string{"has space"})
	require.Error(t, err)

	_, err = normalizeEventTypes([]string{strconv.Itoa(0) + string(make([]byte, 64))})
	require.Error(t, err, "a type longer than the audit column could never match")

	many := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		many = append(many, "event_type_number_"+strconv.Itoa(index)+"_padded_out_to_be_long")
	}
	_, err = normalizeEventTypes(many)
	require.Error(t, err, "a selection longer than its column must be refused, not truncated")
}

func TestFanOutReachesEverySubscriptionBeyondOnePassLimit(t *testing.T) {
	// One pass catches up at most FanOutSubscriptions of them. Restarting at the
	// front every pass would leave the ones past the limit unfed forever, so
	// consecutive passes have to walk on and wrap around.
	env := newWebhookEnvironment(t)
	env.service.cfg.FanOutSubscriptions = 2
	items := []Subscription{env.subscribe(newReceiver(t)), env.subscribe(newReceiver(t)), env.subscribe(newReceiver(t))}
	env.stream.append(guid.ID(1), "principal_created")

	for pass := 0; pass < 2; pass++ {
		require.NoError(t, env.service.FanOut(t.Context()))
	}
	for _, item := range items {
		assert.Len(t, env.deliveries(item.ID), 1, "subscription %s is owed the event", item.Name)
	}

	// The cursor wrapped, so the front of the order is reached again for the
	// next event rather than only the tail.
	env.stream.append(guid.ID(1), "principal_created")
	for pass := 0; pass < 2; pass++ {
		require.NoError(t, env.service.FanOut(t.Context()))
	}
	for _, item := range items {
		assert.Len(t, env.deliveries(item.ID), 2, "subscription %s is owed both events", item.Name)
	}
}

func TestFanOutWrapsWhenThePageEndsExactlyAtTheLastSubscription(t *testing.T) {
	// Two subscriptions and a limit of two: the first pass ends on the last one,
	// and the next pass must start over rather than find nothing after it.
	env := newWebhookEnvironment(t)
	env.service.cfg.FanOutSubscriptions = 2
	items := []Subscription{env.subscribe(newReceiver(t)), env.subscribe(newReceiver(t))}
	require.NoError(t, env.service.FanOut(t.Context()))

	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	for _, item := range items {
		assert.Len(t, env.deliveries(item.ID), 1, "subscription %s is owed the event on the very next pass", item.Name)
	}
}

func TestFanOutMovesPastASubscriptionThatFails(t *testing.T) {
	// A pass that stops on a failing subscription must not keep stopping on it:
	// the ones after it would never be reached.
	env := newWebhookEnvironment(t)
	env.service.cfg.FanOutSubscriptions = 1
	env.subscribe(newReceiver(t))
	second := env.subscribe(newReceiver(t))
	env.stream.append(guid.ID(1), "principal_created")

	env.stream.mu.Lock()
	env.stream.err = assert.AnError
	env.stream.mu.Unlock()
	require.Error(t, env.service.FanOut(t.Context()))
	env.stream.mu.Lock()
	env.stream.err = nil
	env.stream.mu.Unlock()

	require.NoError(t, env.service.FanOut(t.Context()))
	assert.Len(t, env.deliveries(second.ID), 1, "the pass after a failure continues with the next subscription")
}

// blockingReceiver holds its first request until the caller gives up, which is
// how a delivery is caught with its POST in flight. Later requests succeed.
func blockingReceiver(t *testing.T) (*httptest.Server, <-chan struct{}, *atomic.Int64) {
	t.Helper()
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	var answered atomic.Int64
	var block atomic.Bool
	block.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if block.CompareAndSwap(true, false) {
			arrived <- struct{}{}
			select {
			case <-request.Context().Done():
			case <-release:
			}
			return
		}
		answered.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	return server, arrived, &answered
}

func TestDeliveryCanceledMidRequestIsStillDelivered(t *testing.T) {
	// The worker is stopped while its POST is in flight. The outcome is written
	// on a context the cancellation does not reach, so the row is released for a
	// retry instead of staying claimed with nobody left to finish it.
	env := newWebhookEnvironment(t)
	server, arrived, answered := blockingReceiver(t)
	item, err := env.service.CreateSubscription(t.Context(), guid.ID(1), SubscriptionInput{Name: "slow", URL: server.URL})
	require.NoError(t, err)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- env.service.DeliverDue(ctx) }()
	<-arrived
	cancel()
	<-done

	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	assert.Equal(t, DeliveryPending, owed[0].Status, "the canceled attempt released its claim")

	env.service.now = func() time.Time { return webhookTestNow.Add(time.Minute) }
	require.NoError(t, env.service.DeliverDue(t.Context()))
	owed = env.deliveries(item.ID)
	assert.Equal(t, DeliveryDelivered, owed[0].Status)
	assert.EqualValues(t, 1, answered.Load())
}

func TestAbandonedClaimIsReclaimedAfterItsLeaseExpires(t *testing.T) {
	// A worker that claimed a row and then died (kill -9, lost host) never
	// writes an outcome. Once the lease is over the row is offered again.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	owed := env.deliveries(item.ID)
	require.Len(t, owed, 1)
	claimed, err := env.service.repo.claimDelivery(t.Context(), owed[0].ID, 0, webhookTestNow)
	require.NoError(t, err)
	require.True(t, claimed)

	// Inside the lease the claim is respected: its holder might still be
	// mid-request, and sending again would duplicate it for no reason.
	lease := 2 * env.service.cfg.HTTPTimeout
	env.service.now = func() time.Time { return webhookTestNow.Add(lease - time.Second) }
	require.NoError(t, env.service.DeliverDue(t.Context()))
	assert.Zero(t, target.count())
	assert.Equal(t, DeliveryDelivering, env.deliveries(item.ID)[0].Status)
	assert.Zero(t, env.service.reclaimed.Load())

	env.service.now = func() time.Time { return webhookTestNow.Add(lease + time.Second) }
	require.NoError(t, env.service.DeliverDue(t.Context()))
	assert.Equal(t, 1, target.count())
	owed = env.deliveries(item.ID)
	assert.Equal(t, DeliveryDelivered, owed[0].Status)
	assert.Equal(t, 2, owed[0].Attempts, "the dead worker's attempt still counts")
	assert.EqualValues(t, 1, env.service.reclaimed.Load())
	assert.EqualValues(t, 1, env.service.delivered.Load())
}

func TestDeliverDueReportsAClaimItCannotReclaim(t *testing.T) {
	env := newWebhookEnvironment(t)
	require.NoError(t, env.db.Exec("DROP TABLE iam_webhook_deliveries").Error)
	err := env.service.DeliverDue(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reclaim stale webhook deliveries")
}

// supersedingReceiver plays the part of a second replica: while the first
// worker's POST is in flight, the lease is taken back and claimed again.
func supersedingReceiver(t *testing.T, env *webhookEnvironment, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		later := webhookTestNow.Add(time.Hour)
		reclaimed, err := env.service.repo.resetStaleDeliveries(request.Context(), later, later)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, reclaimed)
		id, err := guid.Parse(request.Header.Get(HeaderDelivery))
		assert.NoError(t, err)
		claimed, err := env.service.repo.claimDelivery(request.Context(), id, 1, later)
		assert.NoError(t, err)
		assert.True(t, claimed)
		writer.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLateOutcomeFromASupersededClaimIsDiscarded(t *testing.T) {
	// The first worker overran its lease; the row was reclaimed and claimed
	// again while its POST was still in flight. Neither its success nor its
	// failure may overwrite the claim another worker now holds.
	for name, status := range map[string]int{"success": http.StatusOK, "failure": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			env := newWebhookEnvironment(t)
			env.service.cfg.MaxAttempts = 1
			server := supersedingReceiver(t, env, status)
			item, err := env.service.CreateSubscription(t.Context(), guid.ID(1), SubscriptionInput{Name: "late", URL: server.URL})
			require.NoError(t, err)
			env.stream.append(guid.ID(1), "principal_created")
			require.NoError(t, env.service.FanOut(t.Context()))

			require.NoError(t, env.service.DeliverDue(t.Context()))

			owed := env.deliveries(item.ID)
			require.Len(t, owed, 1)
			assert.Equal(t, DeliveryDelivering, owed[0].Status, "the second claim is still the one in force")
			assert.Equal(t, 2, owed[0].Attempts)
			assert.Nil(t, owed[0].DeliveredAt)
			assert.Empty(t, owed[0].LastError)
			assert.Zero(t, env.service.delivered.Load(), "a discarded outcome is not counted as delivered")
			assert.Zero(t, env.service.abandoned.Load(), "a discarded outcome is not counted as given up")
		})
	}
}
