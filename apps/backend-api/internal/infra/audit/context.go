package audit

import (
	"context"
	"maps"
	"sync"
)

// inflightKey keys the in-flight audit entry stored on the request context.
// The entry is a pointer so business code can enrich it from any depth via
// Annotate without replacing the context value.
type inflightKey struct{}

// actorKey keys the request actor placed on the context by authentication
// middleware. Protocol adapters and audit.Do read it when opening an event.
type actorKey struct{}

// inflight is the mutable audit state for one in-flight operation. It is
// created by the HTTP middleware, the gRPC interceptors, or audit.Do, and
// finalized by the same adapter once the operation completes.
//
// mu guards event against concurrent access. Annotate may be called from any
// depth of the operation, including goroutines that outlive the handler; the
// finalizers snapshot the event under the same lock so enrichment never races
// with the record path.
type inflight struct {
	mu    sync.Mutex
	event *Event

	// reqBody and reqSize capture the request body before the handler sees it.
	reqBody    []byte
	reqSize    int64
	reqLimited bool
}

// WithActor attaches the authenticated principal to the context. Authentication
// middleware that runs before the audit middleware uses this; middleware that
// runs inside the audit middleware uses Annotate(ctx, audit.Actor(a)) instead.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFromContext returns the actor attached by WithActor. It is safe to call
// on any context; the zero Actor is returned when unset.
func ActorFromContext(ctx context.Context) Actor {
	if actor, ok := ctx.Value(actorKey{}).(Actor); ok {
		return actor
	}
	return Actor{}
}

// Option enriches the in-flight event of the surrounding operation.
type Option func(*Event)

// Action sets the domain verb, e.g. "order.cancel".
func Action(value string) Option {
	return func(event *Event) { event.Action = value }
}

// Resource identifies the affected resource.
func Resource(kind, id string) Option {
	return func(event *Event) {
		event.ResourceType = kind
		event.ResourceID = id
	}
}

// SetAccess overrides the operation's read/write classification. Use it to
// force a sensitive read (e.g. an export) to be recorded.
func SetAccess(access Access) Option {
	return func(event *Event) { event.Access = access }
}

// SetActor overrides the operation's principal.
func SetActor(actor Actor) Option {
	return func(event *Event) { event.Actor = actor }
}

// Tenant sets the tenant that scoped the operation.
func Tenant(id string) Option {
	return func(event *Event) { event.Actor.TenantID = id }
}

// Session attaches the client session identifier.
func Session(id string) Option {
	return func(event *Event) { event.Client.SessionID = id }
}

// Metadata attaches one key/value context item.
func Metadata(key, value string) Option {
	return func(event *Event) {
		if event.Metadata == nil {
			event.Metadata = make(map[string]string, 1)
		}
		event.Metadata[key] = value
	}
}

// Annotate enriches the in-flight operation opened by the surrounding adapter.
// It is a no-op when the context carries no in-flight audit entry, so business
// code may call it unconditionally without knowing which entry point produced
// the operation.
func Annotate(ctx context.Context, options ...Option) {
	entry, ok := inflightFromContext(ctx)
	if !ok || len(options) == 0 {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	for _, option := range options {
		if option != nil {
			option(entry.event)
		}
	}
}

// snapshot returns a copy of the event taken under the inflight lock. The
// metadata map is cloned so a concurrent Annotate never races with the recorder
// reading it. Finalizers snapshot before reading any event field.
func (entry *inflight) snapshot() Event {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	event := *entry.event
	if event.Metadata != nil {
		event.Metadata = maps.Clone(event.Metadata)
	}
	return event
}

// entryFromContext returns the in-flight audit entry, if any.
func inflightFromContext(ctx context.Context) (*inflight, bool) {
	entry, ok := ctx.Value(inflightKey{}).(*inflight)
	return entry, ok
}

// withInflight stores the in-flight entry on a copy of the context.
func withInflight(ctx context.Context, entry *inflight) context.Context {
	return context.WithValue(ctx, inflightKey{}, entry)
}

// actorOf returns the actor for a new event: the explicit override if set,
// otherwise the actor attached to the context, otherwise anonymous.
func actorOf(ctx context.Context, override Actor) Actor {
	if override.Type != "" || override.ID != "" || override.Name != "" {
		return override
	}
	if actor := ActorFromContext(ctx); actor.Type != "" || actor.ID != "" || actor.Name != "" {
		return actor
	}
	return Actor{Type: ActorAnonymous}
}
