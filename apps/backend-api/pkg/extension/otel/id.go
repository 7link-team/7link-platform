package otelx

import (
	"context"
	"crypto/rand"
	"strings"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// IDGenerator returns a UUID or a 32-character hexadecimal OpenTelemetry trace ID.
type IDGenerator func() string

func defaultIDGenerator() string {
	return uuid.NewString()
}

type traceIDGenerator struct {
	generate IDGenerator
}

var _ sdktrace.IDGenerator = (*traceIDGenerator)(nil)

func (g *traceIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	traceID, ok := parseTraceID(g.generate())
	if !ok {
		traceID, _ = parseTraceID(defaultIDGenerator())
	}
	return traceID, g.NewSpanID(ctx, traceID)
}

func (*traceIDGenerator) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	for {
		var spanID trace.SpanID
		if _, err := rand.Read(spanID[:]); err == nil && spanID.IsValid() {
			return spanID
		}
	}
}

func parseTraceID(value string) (trace.TraceID, bool) {
	value = strings.TrimSpace(value)
	if traceID, err := trace.TraceIDFromHex(value); err == nil && traceID.IsValid() {
		return traceID, true
	}

	id, err := uuid.Parse(value)
	if err != nil {
		return trace.TraceID{}, false
	}
	traceID := trace.TraceID(id)
	return traceID, traceID.IsValid()
}
