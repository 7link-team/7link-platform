package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

type inboxRealtimeCatalog struct{ subscribers []events.Subscriber }

func (c *inboxRealtimeCatalog) AddSubscribers(_ string, rows ...events.Subscriber) error {
	c.subscribers = append(c.subscribers, rows...)
	return nil
}

func TestInboxRealtimeHintsAreTenantScopedContentFreeAndAfterCommit(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	var authorize realtime.TopicAuthorizer
	var hints []string
	catalog := &inboxRealtimeCatalog{}
	require.NoError(t, module.Provide[realtime.TopicAuthorizers](s, realtime.TopicAuthorizers{Register: func(kind string, fn realtime.TopicAuthorizer) error {
		require.Equal(t, "governance_inbox", kind)
		authorize = fn
		return nil
	}}))
	require.NoError(t, module.Provide[realtime.Broadcaster](s, func(_ context.Context, topic string, value any) error {
		message := value.(realtime.Message)
		require.Equal(t, "governance.inbox_changed", message.Type)
		require.Empty(t, message.Payload)
		hints = append(hints, topic)
		return nil
	}))
	require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindInboxRealtime(s))
	require.Len(t, catalog.subscribers, 1)
	sub := catalog.subscribers[0]
	require.Equal(t, events.Transactional, sub.Mode)
	require.NotContains(t, sub.EventTypes, "realtime.*", "invalidation must not loop on its own hint")
	require.NoError(t, sub.Handle(t.Context(), nil, events.Envelope{TenantID: 7, Data: []byte(`{"private":"secret"}`)}))
	require.Empty(t, hints, "uncommitted handler must never broadcast")
	sub.AfterCommit(t.Context(), events.Envelope{TenantID: 7})
	sub.AfterCommit(t.Context(), events.Envelope{TenantID: 0})
	require.Equal(t, []string{"tenant/7/governance_inbox"}, hints)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11, TenantID: 7})
	for _, test := range []struct {
		tenant, principal guid.ID
		topic             realtime.Topic
		allowed           bool
	}{
		{7, 11, realtime.Topic{TenantID: 7, Kind: "governance_inbox", Collection: true}, true},
		{8, 11, realtime.Topic{TenantID: 8, Kind: "governance_inbox", Collection: true}, false},
		{7, 12, realtime.Topic{TenantID: 7, Kind: "governance_inbox", Collection: true}, false},
		{7, 11, realtime.Topic{TenantID: 7, Kind: "governance_inbox", ID: 11}, false},
	} {
		allowed, err := authorize(ctx, test.tenant, test.principal, test.topic)
		require.NoError(t, err)
		require.Equal(t, test.allowed, allowed)
	}
	// The actual event catalog validates this registration alongside all peers.
	actual := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	require.NoError(t, actual.AddSubscribers(Name, sub))
}
