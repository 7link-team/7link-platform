package otelx

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	TraceIDHeader     = "X-Trace-ID"
	TraceparentHeader = "traceparent"
)

type traceIDPropagator struct {
	idGenerator *traceIDGenerator
}

func (p traceIDPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		carrier.Set(TraceIDHeader, spanContext.TraceID().String())
	}
}

func (p traceIDPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	traceID, ok := parseTraceID(carrier.Get(TraceIDHeader))
	if !ok {
		return ctx
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     p.idGenerator.NewSpanID(ctx, traceID),
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, spanContext)
}

func (traceIDPropagator) Fields() []string {
	return []string{TraceIDHeader}
}
