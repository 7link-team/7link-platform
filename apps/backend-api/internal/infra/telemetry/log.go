package telemetry

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/trace"
)

func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func SpanID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.SpanID().String()
}

func LogHTTP(ctx context.Context, request *http.Request, metrics httpsnoop.Metrics) {
	slog.InfoContext(ctx, "HTTP request completed",
		"method", request.Method,
		"path", request.URL.Path,
		"status", metrics.Code,
		"duration_ms", float64(metrics.Duration.Nanoseconds())/1e6,
		"bytes", metrics.Written,
	)
}
