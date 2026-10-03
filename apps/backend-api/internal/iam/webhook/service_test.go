package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/webhook/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var webhookTestNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// webhookTestIDs hands out increasing identifiers, which is what the real source
// does and what the delivery order assertions rely on.
type webhookTestIDs struct{ next atomic.Int64 }

func (ids *webhookTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

// fakeStream is an audit chain under the test's control. Its events carry no
// detail, address or agent -- the real projection has no such fields, and a fake
// that invented them would hide the thing that keeps them out.
type fakeStream struct {
	mu     sync.Mutex
	events map[guid.ID][]audit.StreamEvent
	err    error
	reads  atomic.Int64
}

func newFakeStream() *fakeStream {
	return &fakeStream{events: map[guid.ID][]audit.StreamEvent{}}
}

func (f *fakeStream) append(tenantID guid.ID, eventType string) audit.StreamEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	sequence := int64(len(f.events[tenantID]) + 1)
	event := audit.StreamEvent{
		ID: guid.ID(1000 + sequence), TenantID: tenantID, Sequence: sequence,
		EventType: eventType, TargetType: "principal", TargetID: guid.ID(7),
		PrincipalID: guid.ID(9), Outcome: "success", OccurredAt: webhookTestNow,
	}
	f.events[tenantID] = append(f.events[tenantID], event)
	return event
}

func (f *fakeStream) read(_ context.Context, tenantID guid.ID, afterSequence int64, limit int) ([]audit.StreamEvent, error) {
	f.reads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := []audit.StreamEvent{}
	for _, event := range f.events[tenantID] {
		if event.Sequence > afterSequence && len(out) < limit {
			out = append(out, event)
		}
	}
	return out, nil
}

// receiver records what arrived and answers however a test tells it to.
type receiver struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	status   atomic.Int64
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	target := &receiver{}
	target.status.Store(http.StatusOK)
	target.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		target.mu.Lock()
		target.requests = append(target.requests, request.Clone(context.Background()))
		target.bodies = append(target.bodies, string(body))
		target.mu.Unlock()
		writer.WriteHeader(int(target.status.Load()))
	}))
	t.Cleanup(target.server.Close)
	return target
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) at(index int) (*http.Request, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[index], r.bodies[index]
}

type webhookEnvironment struct {
	t          *testing.T
	db         *gorm.DB
	runtime    *appenv.Runtime
	service    *Service
	stream     *fakeStream
	prefix     string
	subscribed int
}

func newWebhookEnvironment(t *testing.T, prefixes ...string) *webhookEnvironment {
	t.Helper()
	prefix := ""
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on",
		filepath.ToSlash(filepath.Join(t.TempDir(), "webhook.sqlite")))
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })

	runtime := appenv.NewRuntime(appenv.NewIdentity("webhook-test", "1"),
		appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	env := &webhookEnvironment{t: t, db: router.Write(), runtime: runtime, stream: newFakeStream(), prefix: prefix}
	for _, row := range []any{&model.Subscription{}, &model.Delivery{}} {
		table := prefix + tableNameOf(row)
		require.NoError(t, env.db.Table(table).AutoMigrate(row))
	}
	// The real migration declares both of these. Without them the test schema
	// would admit rows production refuses, which is how a duplicate-name test
	// passes against storage and fails against a deployment.
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefix+
		"test_webhook_delivery_event ON "+prefix+"iam_webhook_deliveries (subscription_id, event_id)").Error)
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefix+
		"test_webhook_subscription_name ON "+prefix+"iam_webhook_subscriptions (tenant_id, name)").Error)

	keys, err := keyringx.New(webhookKeyDomain, base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	require.NoError(t, err)
	policy, err := httpx.ParsePolicy(true, []string{"127.0.0.0/8"})
	require.NoError(t, err)

	env.service = NewService(runtime, env.stream.read, Config{
		HTTPTimeout: 5 * time.Second, RetryBase: time.Second, MaxAttempts: 3,
		DeliveryBatch: 50, FanOutBatch: 200, FanOutSubscriptions: 200,
	}, keys, &webhookTestIDs{}, policy)
	env.service.now = func() time.Time { return webhookTestNow }
	return env
}

func tableNameOf(row any) string {
	switch row.(type) {
	case *model.Subscription:
		return model.Subscription{}.TableName()
	default:
		return model.Delivery{}.TableName()
	}
}

func (e *webhookEnvironment) subscribe(target *receiver, eventTypes ...string) Subscription {
	e.t.Helper()
	name := fmt.Sprintf("receiver-%d", e.subscribed)
	e.subscribed++
	item, err := e.service.CreateSubscription(e.t.Context(), guid.ID(1), SubscriptionInput{
		Name: name, URL: target.server.URL, Secret: "the-signing-secret", EventTypes: eventTypes,
	})
	require.NoError(e.t, err)
	return item
}

func (e *webhookEnvironment) deliveries(subscriptionID guid.ID) []Delivery {
	e.t.Helper()
	items, _, err := e.service.ListDeliveries(e.t.Context(), guid.ID(1), subscriptionID, 0, 200)
	require.NoError(e.t, err)
	return items
}

func TestSubscriptionStartsAtTheCurrentHead(t *testing.T) {
	// Registering a receiver asks to hear what happens next. Starting at zero
	// would send a new endpoint the tenant's entire history on its first pass.
	env := newWebhookEnvironment(t)
	for index := 0; index < 3; index++ {
		env.stream.append(guid.ID(1), "principal_created")
	}

	item := env.subscribe(newReceiver(t))
	assert.EqualValues(t, 3, item.AfterSequence)

	require.NoError(t, env.service.FanOut(t.Context()))
	assert.Empty(t, env.deliveries(item.ID), "nothing that happened before the subscription existed is owed")
}

func TestFanOutOwesEachMatchingEventOnce(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target, "role_granted")

	env.stream.append(guid.ID(1), "role_granted")
	env.stream.append(guid.ID(1), "principal_created")
	env.stream.append(guid.ID(1), "role_granted")

	require.NoError(t, env.service.FanOut(t.Context()))
	owed := env.deliveries(item.ID)
	require.Len(t, owed, 2, "only the selected type is owed")
	for _, delivery := range owed {
		assert.Equal(t, "role_granted", delivery.EventType)
	}

	// Running the pass again must not owe the same events twice. The cursor
	// advanced past the skipped event as well, which is what stops it being
	// reconsidered forever.
	require.NoError(t, env.service.FanOut(t.Context()))
	assert.Len(t, env.deliveries(item.ID), 2)

	stored, err := env.service.GetSubscription(t.Context(), guid.ID(1), item.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, stored.AfterSequence, "the cursor passes events the subscription did not want")
}

func TestDeliveryCarriesNoPersonalData(t *testing.T) {
	// The payload is audit's projection verbatim. This asserts the contract a
	// receiver sees, so adding a field that holds a person's data to StreamEvent
	// would fail here rather than at somebody else's endpoint.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")

	require.NoError(t, env.service.FanOut(t.Context()))
	require.NoError(t, env.service.DeliverDue(t.Context()))
	require.Equal(t, 1, target.count())

	_, body := target.at(0)
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &sent))
	event, ok := sent["event"].(map[string]any)
	require.True(t, ok)

	for _, forbidden := range []string{"detail", "ip_address", "user_agent", "email", "display_name", "name"} {
		assert.NotContains(t, event, forbidden, "a receiver must not be handed %s", forbidden)
	}
	assert.Equal(t, "principal_created", event["event_type"])
	assert.Equal(t, "success", event["outcome"])
	assert.NotEmpty(t, event["id"])

	assert.Equal(t, DeliveryDelivered, env.deliveries(item.ID)[0].Status)
}

func TestDeliverySignsWhatItSendsAndIdentifiesItself(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")

	require.NoError(t, env.service.FanOut(t.Context()))
	require.NoError(t, env.service.DeliverDue(t.Context()))
	require.Equal(t, 1, target.count())

	request, body := target.at(0)
	timestamp := request.Header.Get(HeaderTimestamp)
	require.NotEmpty(t, timestamp)
	assert.Equal(t, sign([]byte("the-signing-secret"), webhookTestNow.Unix(), []byte(body)),
		request.Header.Get(HeaderSignature), "the receiver must be able to verify what it was sent")

	assert.NotEmpty(t, request.Header.Get(HeaderDelivery), "a receiver needs a key to deduplicate on")
	assert.NotEmpty(t, request.Header.Get(HeaderEventID))
	assert.Equal(t, "webhook-test/1", request.Header.Get("User-Agent"),
		"the deployment names itself from its runtime identity")
	// No header is named after the deployment. Comparing against the runtime
	// identity states the rule itself -- a protocol header must not carry whoever
	// is sending it -- and holds for any deployment, including one this test was
	// never written against.
	deployment := env.runtime.Identity().Name()
	require.NotEmpty(t, deployment)
	for header := range request.Header {
		assert.NotContains(t, strings.ToLower(header), strings.ToLower(deployment),
			"a protocol header must be named for what it carries, not for who sends it")
	}
}

func TestDeliveryRetriesThenGivesUp(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	target.status.Store(http.StatusInternalServerError)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	// MaxAttempts is 3, so the third failure is terminal and nothing is tried a
	// fourth time.
	for attempt := 1; attempt <= 3; attempt++ {
		env.service.now = func() time.Time { return webhookTestNow.Add(time.Duration(attempt) * time.Hour) }
		require.NoError(t, env.service.DeliverDue(t.Context()))
		owed := env.deliveries(item.ID)
		require.Len(t, owed, 1)
		assert.Equal(t, attempt, owed[0].Attempts)
		if attempt < 3 {
			assert.Equal(t, DeliveryPending, owed[0].Status)
			assert.Contains(t, owed[0].LastError, "500")
		}
	}
	owed := env.deliveries(item.ID)
	assert.Equal(t, DeliveryFailed, owed[0].Status, "the attempts are spent")

	env.service.now = func() time.Time { return webhookTestNow.Add(99 * time.Hour) }
	require.NoError(t, env.service.DeliverDue(t.Context()))
	assert.Equal(t, 3, target.count(), "a failed delivery is not tried again")
}

func TestDeliverySucceedsAfterAReceiverRecovers(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	target.status.Store(http.StatusBadGateway)
	item := env.subscribe(target)
	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	require.NoError(t, env.service.DeliverDue(t.Context()))
	assert.Equal(t, DeliveryPending, env.deliveries(item.ID)[0].Status)

	target.status.Store(http.StatusNoContent)
	env.service.now = func() time.Time { return webhookTestNow.Add(time.Hour) }
	require.NoError(t, env.service.DeliverDue(t.Context()))

	owed := env.deliveries(item.ID)
	assert.Equal(t, DeliveryDelivered, owed[0].Status)
	assert.Empty(t, owed[0].LastError, "a success clears the reason the last attempt failed")
}

func TestDisabledSubscriptionIsNotOfferedEvents(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	item := env.subscribe(target)
	_, err := env.service.UpdateSubscription(t.Context(), guid.ID(1), item.ID, SubscriptionInput{
		Name: "receiver-0", URL: target.server.URL, Status: SubscriptionDisabled,
	})
	require.NoError(t, err)

	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))
	assert.Empty(t, env.deliveries(item.ID))
	assert.Zero(t, target.count())
}

func TestAnotherTenantsEventsAreNeverOffered(t *testing.T) {
	env := newWebhookEnvironment(t, "sl_")
	target := newReceiver(t)
	item := env.subscribe(target)

	env.stream.append(guid.ID(2), "principal_created")
	require.NoError(t, env.service.FanOut(t.Context()))

	assert.Empty(t, env.deliveries(item.ID), "a subscription follows its own tenant's chain only")
	assert.Zero(t, target.count())
}
