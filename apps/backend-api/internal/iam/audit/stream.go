package audit

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/infra/guid"
)

// StreamEvent is an appended audit record with everything personal left behind.
// The detail map, the address and the agent string are not fields here, so a
// consumer outside this module cannot forward them even by mistake: the three
// places a person's data lives in an audit record are the three this projection
// does not select.
//
// That matters because a consumer may be an endpoint somebody else operates. A
// deployment that honors a withdrawal can erase what it holds; it cannot erase
// what it has already handed out. So what leaves here is identifiers and facts
// that name no one: who acted, what was acted on, which way it went, and when.
type StreamEvent struct {
	ID          guid.ID   `json:"id"`
	TenantID    guid.ID   `json:"tenant_id"`
	Sequence    int64     `json:"sequence"`
	EventType   string    `json:"event_type"`
	TargetType  string    `json:"target_type,omitempty"`
	TargetID    guid.ID   `json:"target_id,omitempty"`
	PrincipalID guid.ID   `json:"principal_id,omitempty"`
	Outcome     string    `json:"outcome"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// EventStreamReader hands out one tenant's appended events in sequence order,
// starting after the sequence a consumer has already dealt with. It is the only
// way out of this module's event table for anything but this module.
//
// The chain's sequence is per tenant and gapless, which is what lets a consumer
// keep a single cursor and know it has missed nothing: a reader that returns
// fewer rows than its limit has reached the end of what exists so far.
type EventStreamReader func(ctx context.Context, tenantID guid.ID, afterSequence int64, limit int) ([]StreamEvent, error)

// maxStreamBatch bounds one read regardless of what a consumer asks for, so a
// follower that has fallen far behind cannot pull the whole chain into memory in
// one statement.
const maxStreamBatch = 500

// ReadStream implements EventStreamReader.
func (s *Service) ReadStream(ctx context.Context, tenantID guid.ID, afterSequence int64, limit int) ([]StreamEvent, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUnavailable
	}
	if tenantID.Zero() {
		return nil, fmt.Errorf("%w: tenant is required to read the event stream", ErrInvalidEvent)
	}
	if limit <= 0 || limit > maxStreamBatch {
		limit = maxStreamBatch
	}
	if afterSequence < 0 {
		afterSequence = 0
	}
	return s.repo.readStream(ctx, tenantID, afterSequence, limit)
}
