package webhook

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeCallbacksSignOnlyMetadataAndRetryCurrentPolicy(t *testing.T) {
	e := newWebhookEnvironment(t, "callback_")
	r := newReceiver(t)
	in := SubscriptionInput{Name: "Intake receiver", URL: r.server.URL, Secret: "receiver-signing-secret", PayloadMode: PayloadIntakeMetadata}
	sub, err := e.service.CreateSubscription(t.Context(), 1, in)
	require.NoError(t, err)
	// A later protocol change must never expose richer historical audit payloads.
	in.PayloadMode = PayloadAudit
	_, err = e.service.UpdateSubscription(t.Context(), 1, sub.ID, in)
	require.ErrorIs(t, err, ErrInvalidSubscription)
	in.PayloadMode = PayloadIntakeMetadata
	add := func(target string, id guid.ID) {
		e.stream.mu.Lock()
		defer e.stream.mu.Unlock()
		seq := int64(len(e.stream.events[1]) + 1)
		e.stream.events[1] = append(e.stream.events[1], audit.StreamEvent{ID: guid.ID(1000 + seq), TenantID: 1, Sequence: seq, EventType: "intake.clarification_requested", TargetType: target, TargetID: id, PrincipalID: 987, Outcome: "success", OccurredAt: webhookTestNow})
	}
	add("principal", 777)
	add("intake", 9007199254740993)
	require.NoError(t, e.service.FanOut(t.Context()))
	require.Len(t, e.deliveries(sub.ID), 1)
	r.status.Store(503)
	require.NoError(t, e.service.DeliverDue(t.Context()))
	require.Len(t, r.bodies, 1)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.bodies[0]), &body))
	require.Equal(t, map[string]any{"event_type": "intake.clarification_requested", "target_id": "9007199254740993", "occurred_at": webhookTestNow.Format(time.RFC3339Nano)}, body)
	req := r.requests[0]
	ts, err := strconv.ParseInt(req.Header.Get(HeaderTimestamp), 10, 64)
	require.NoError(t, err)
	require.Equal(t, sign([]byte(in.Secret), ts, []byte(r.bodies[0])), req.Header.Get(HeaderSignature))
	key := req.Header.Get(HeaderDelivery)
	require.NotEmpty(t, key)
	r.status.Store(200)
	e.service.now = func() time.Time { return webhookTestNow.Add(time.Minute) }
	require.NoError(t, e.service.DeliverDue(t.Context()))
	require.Len(t, r.bodies, 2)
	require.Equal(t, r.bodies[0], r.bodies[1])
	require.Equal(t, key, r.requests[1].Header.Get(HeaderDelivery))
	require.Equal(t, DeliveryDelivered, e.deliveries(sub.ID)[0].Status)
	add("intake", 33)
	require.NoError(t, e.service.FanOut(t.Context()))
	in.Status = SubscriptionDisabled
	_, err = e.service.UpdateSubscription(t.Context(), 1, sub.ID, in)
	require.NoError(t, err)
	require.NoError(t, e.service.DeliverDue(t.Context()))
	require.Len(t, r.bodies, 2, "disabling a configured callback must stop queued sends")
}

func TestIntakeCallbackValidationUsesCurrentTenantTransactionAndSignedMode(t *testing.T) {
	e := newWebhookEnvironment(t)
	r := newReceiver(t)
	in := SubscriptionInput{Name: "Intake", URL: r.server.URL, PayloadMode: PayloadIntakeMetadata}
	_, err := e.service.CreateSubscription(t.Context(), 1, in)
	require.ErrorIs(t, err, ErrInvalidSubscription)
	in.Secret = "signing-secret"
	sub, err := e.service.CreateSubscription(t.Context(), 1, in)
	require.NoError(t, err)
	check := func(tenant, id guid.ID) error {
		return e.db.Transaction(func(tx *gorm.DB) error { return e.service.ValidateOn(t.Context(), tx, tenant, id) })
	}
	require.NoError(t, check(1, sub.ID))
	require.ErrorIs(t, check(2, sub.ID), ErrSubscriptionNotFound)
	require.ErrorIs(t, e.service.ValidateOn(t.Context(), e.db, 1, sub.ID), ErrInvalidSubscription)
	in.Status = SubscriptionDisabled
	_, err = e.service.UpdateSubscription(t.Context(), 1, sub.ID, in)
	require.NoError(t, err)
	require.ErrorIs(t, check(1, sub.ID), ErrInvalidSubscription)
	ordinary := e.subscribe(r)
	require.ErrorIs(t, check(1, ordinary.ID), ErrInvalidSubscription)
	require.ErrorIs(t, (*Service)(nil).ValidateOn(context.Background(), nil, 1, sub.ID), ErrUnavailable)
}

func TestIntakeCallbackReceiverIsOptionalAndBindsProviderCapability(t *testing.T) {
	e := newWebhookEnvironment(t)
	m := &Module{service: e.service}
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindIntakeCallbacks(s))
	s = module.NewServices()
	var actual IntakeCallbackSubscriptions
	require.NoError(t, module.Provide[IntakeCallbackReceiver](s, IntakeCallbackReceiver{Bind: func(v IntakeCallbackSubscriptions) error { actual = v; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindIntakeCallbacks(s))
	require.Same(t, e.service, actual)
}
