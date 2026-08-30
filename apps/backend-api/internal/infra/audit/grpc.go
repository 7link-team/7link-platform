package audit

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// UnaryServerInterceptor audits one unary gRPC call. The method name is
// classified read/write by prefix; write calls capture their request message
// (serialized with protojson, redacted, size-capped). On failure the gRPC
// status code and message are recorded; on success only the outcome, mirroring
// the HTTP success-summary rule.
//
// Chain it after the telemetry interceptor so the call's span is on the context.
func (m *Module) UnaryServerInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if !m.enabled() {
		return handler(ctx, request)
	}
	access := classifyGRPC(info.FullMethod)
	entry := &inflight{
		event: &Event{
			OccurredAt: time.Now().UTC(),
			Entry:      KindGRPC,
			Operation:  info.FullMethod,
			Access:     access,
			Actor:      actorOf(ctx, Actor{}),
			Client:     clientFromGRPC(ctx),
			Target:     Target{Method: "unary", Route: info.FullMethod},
		},
	}
	ctx = withInflight(ctx, entry)

	if access == AccessWrite {
		if message, ok := request.(proto.Message); ok {
			// Check the encoded size before marshaling so an oversized request
			// is never materialized in memory just to be truncated.
			max := m.maxBodyBytes()
			if size := proto.Size(message); max > 0 && size > max {
				entry.reqSize = int64(size)
				entry.reqLimited = true
			} else if raw, err := protojson.Marshal(message); err == nil {
				entry.reqSize = int64(len(raw))
				if max > 0 && len(raw) > max {
					entry.reqLimited = true
				} else {
					entry.reqBody = raw
				}
			}
		}
	}

	started := time.Now()
	response, err := handler(ctx, request)
	entry.mu.Lock()
	entry.event.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	entry.mu.Unlock()

	m.finalizeGRPC(ctx, entry, err)
	return response, err
}

// StreamServerInterceptor audits a gRPC stream as one operation. Streaming RPCs
// are classified by method name like unary calls; the session is recorded once
// when the handler returns. Per-message bodies are not captured.
func (m *Module) StreamServerInterceptor(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if !m.enabled() {
		return handler(service, stream)
	}
	ctx := stream.Context()
	access := classifyGRPC(info.FullMethod)
	entry := &inflight{
		event: &Event{
			OccurredAt: time.Now().UTC(),
			Entry:      KindGRPC,
			Operation:  info.FullMethod,
			Access:     access,
			Actor:      actorOf(ctx, Actor{}),
			Client:     clientFromGRPC(ctx),
			Target:     Target{Method: "stream", Route: info.FullMethod},
		},
	}
	ctx = withInflight(ctx, entry)
	wrapped := &serverStream{ServerStream: stream, ctx: ctx}

	started := time.Now()
	err := handler(service, wrapped)
	entry.mu.Lock()
	entry.event.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	entry.mu.Unlock()

	m.finalizeGRPC(ctx, entry, err)
	return err
}

// serverStream re-exposes the stream with the audit context so business code
// inside the handler can call audit.Annotate(stream.Context(), ...).
type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *serverStream) Context() context.Context { return stream.ctx }

// finalizeGRPC builds the persisted event for a gRPC call.
func (m *Module) finalizeGRPC(ctx context.Context, entry *inflight, err error) {
	// Snapshot under the inflight lock so a concurrent Annotate cannot race with
	// the fields read and recorded below.
	event := entry.snapshot()

	// Respect a per-request access override from Annotate.
	if !(event.Access == AccessWrite || m.recordReads()) {
		return
	}

	if err != nil {
		event.Outcome = OutcomeFailure
		grpcStatus, _ := status.FromError(err)
		event.StatusCode = int(grpcStatus.Code())
		event.Error = grpcStatus.Message()
		if event.Error == "" {
			event.Error = err.Error()
		}
	} else {
		event.Outcome = OutcomeSuccess
		event.StatusCode = int(codes.OK)
		event.Response = Payload{Omitted: OmittedSuccessSummary}
	}

	if entry.reqBody != nil {
		body, truncated, redactErr := m.redactAndTruncate(entry.reqBody)
		if redactErr != nil {
			event.Request = Payload{Size: entry.reqSize, Omitted: OmittedInvalidJSON}
			m.record(ctx, &event)
			return
		}
		omitted := OmittedNone
		if entry.reqLimited {
			omitted = OmittedTooLarge
		}
		event.Request = Payload{
			Body:      body,
			Size:      entry.reqSize,
			Truncated: truncated || entry.reqLimited,
			Omitted:   omitted,
		}
	} else if entry.reqLimited {
		// Oversized request: skipped marshal, record the size and omission.
		event.Request = Payload{Size: entry.reqSize, Truncated: true, Omitted: OmittedTooLarge}
	}

	m.record(ctx, &event)
}
