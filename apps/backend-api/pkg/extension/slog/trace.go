package slogx

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// NewText creates a context-aware text logger. The writer may be an
// io.MultiWriter, including a rolling-file writer, without changing this layer.
func NewText(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	return slog.New(NewTraceHandler(slog.NewTextHandler(writer, options)))
}

// NewTraceHandler decorates any slog handler with OpenTelemetry trace fields.
func NewTraceHandler(next slog.Handler) slog.Handler {
	return &traceHandler{next: next}
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}
