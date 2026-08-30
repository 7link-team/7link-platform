package audit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnotateIsNoOpWithoutInflight(t *testing.T) {
	ctx := context.Background()
	// Must not panic and must not create anything.
	assert.NotPanics(t, func() {
		Annotate(ctx, Action("order.cancel"), Resource("order", "42"), SetAccess(AccessWrite), Metadata("k", "v"))
	})
}

func TestAnnotateEnrichesInflight(t *testing.T) {
	event := &Event{}
	ctx := withInflight(context.Background(), &inflight{event: event})
	Annotate(ctx,
		Action("order.cancel"),
		Resource("order", "42"),
		SetAccess(AccessWrite),
		SetActor(Actor{ID: "u1", Type: ActorUser, Name: "alice", TenantID: "t1"}),
		Session("sess-1"),
		Metadata("region", "cn"),
	)
	assert.Equal(t, "order.cancel", event.Action)
	assert.Equal(t, "order", event.ResourceType)
	assert.Equal(t, "42", event.ResourceID)
	assert.Equal(t, AccessWrite, event.Access)
	assert.Equal(t, "u1", event.Actor.ID)
	assert.Equal(t, ActorUser, event.Actor.Type)
	assert.Equal(t, "sess-1", event.Client.SessionID)
	assert.Equal(t, "cn", event.Metadata["region"])
}

func TestWithActorAndActorFromContext(t *testing.T) {
	ctx := WithActor(context.Background(), Actor{ID: "u9", Type: ActorUser, Name: "bob"})
	actor := ActorFromContext(ctx)
	assert.Equal(t, "u9", actor.ID)
	assert.Equal(t, ActorUser, actor.Type)

	// Unset context yields the zero actor.
	assert.Equal(t, Actor{}, ActorFromContext(context.Background()))
	// Nil option is a no-op.
	assert.NotPanics(t, func() { Annotate(ctx, nil) })
}

func TestActorOfPrecedence(t *testing.T) {
	ctx := WithActor(context.Background(), Actor{ID: "ctx", Type: ActorUser})
	// Explicit override wins.
	assert.Equal(t, "override", actorOf(ctx, Actor{ID: "override", Type: ActorService}).ID)
	// Context actor used when no override.
	assert.Equal(t, "ctx", actorOf(ctx, Actor{}).ID)
	// Anonymous fallback.
	assert.Equal(t, ActorAnonymous, actorOf(context.Background(), Actor{}).Type)
}

func TestInflightFromContext(t *testing.T) {
	entry := &inflight{event: &Event{}}
	ctx := withInflight(context.Background(), entry)
	found, ok := inflightFromContext(ctx)
	require.True(t, ok)
	assert.Same(t, entry, found)
	_, ok = inflightFromContext(context.Background())
	assert.False(t, ok)
}

func TestMetadataGrowsMap(t *testing.T) {
	event := &Event{}
	ctx := withInflight(context.Background(), &inflight{event: event})
	Annotate(ctx, Metadata("a", "1"), Metadata("b", "2"))
	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, event.Metadata)
}

func TestTenantOption(t *testing.T) {
	event := &Event{}
	ctx := withInflight(context.Background(), &inflight{event: event})
	Annotate(ctx, Tenant("t-42"))
	assert.Equal(t, "t-42", event.Actor.TenantID)
}
