package audit

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Spec describes one operation for the generic wrapper. It is the smallest
// amount of identity a producer must supply; everything else is enriched by the
// Recorder.
type Spec struct {
	// Entry names the transport (job, schedule, message, ...). Defaults to
	// internal.
	Entry EntryKind
	// Operation is the human-readable identity, e.g. "inventory.reconcile".
	Operation string
	// Access classifies the operation. Defaults to write.
	Access Access
	// Action is the domain verb, e.g. "inventory.reconcile".
	Action string
	// ResourceType and ResourceID identify the affected resource.
	ResourceType string
	ResourceID   string
	// Actor is the principal. Falls back to the context actor, then anonymous.
	Actor Actor
	// Target identifies the server-side route and method, if applicable.
	Target Target
	// Metadata carries free-form context.
	Metadata map[string]string
}

// Do runs fn as one audited operation. The operation's in-flight entry is placed
// on the context, so fn can enrich it with audit.Annotate from any depth. The
// entry stays in effect until fn returns; Do then finalizes and records it.
//
// Do is the entry point for transports the protocol adapters do not cover yet —
// cron jobs, distributed tasks, queue consumers. It is deliberately generic so
// new transports require no changes to the audit core.
func Do[T any](
	ctx context.Context,
	recorder Recorder,
	spec Spec,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	entry := openSpec(ctx, spec)
	ctx = withInflight(ctx, entry)

	started := time.Now()
	result, err := fn(ctx)

	// finalizeGeneric snapshots the in-flight event under its lock; the copy is
	// what gets recorded, so a concurrent Annotate cannot race with the flush.
	event := finalizeGeneric(entry, err)
	event.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	if recorder != nil {
		recorder.Record(ctx, event)
	}
	return result, err
}

// Run is Do for operations that produce no value. Use it for fire-and-forget
// jobs and scheduled tasks.
func Run(
	ctx context.Context,
	recorder Recorder,
	spec Spec,
	fn func(ctx context.Context) error,
) error {
	_, err := Do(ctx, recorder, spec, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// openSpec builds the in-flight entry for a generic operation.
func openSpec(ctx context.Context, spec Spec) *inflight {
	access := spec.Access
	if access == "" {
		access = AccessWrite
	}
	entry := spec.Entry
	if entry == "" {
		entry = KindInternal
	}
	operation := spec.Operation
	if operation == "" {
		operation = string(entry) + ".operation"
	}
	metadata := spec.Metadata
	if len(metadata) == 0 {
		metadata = nil
	}
	return &inflight{
		event: &Event{
			OccurredAt:   time.Now().UTC(),
			Entry:        entry,
			Operation:    operation,
			Access:       access,
			Action:       spec.Action,
			ResourceType: spec.ResourceType,
			ResourceID:   spec.ResourceID,
			Actor:        actorOf(ctx, spec.Actor),
			Target:       spec.Target,
			Metadata:     metadata,
		},
	}
}

// finalizeGeneric marks an operation's outcome based on the function error and
// captures the error detail when it carries a gRPC status. It returns a
// snapshot of the event, safe to record after the in-flight entry is released.
func finalizeGeneric(entry *inflight, err error) Event {
	event := entry.snapshot()
	if err != nil {
		event.Outcome = OutcomeFailure
		event.Error = err.Error()
		if grpcStatus, ok := status.FromError(err); ok && grpcStatus.Code() != codes.OK {
			event.StatusCode = int(grpcStatus.Code())
		}
		return event
	}
	event.Outcome = OutcomeSuccess
	return event
}
