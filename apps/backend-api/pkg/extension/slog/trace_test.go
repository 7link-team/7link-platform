package slogx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	otelx "7link.com/platform/pkg/extension/otel"
	slogx "7link.com/platform/pkg/extension/slog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceHandlerAddsTraceAndSpanIDs(t *testing.T) {
	const traceID = "123e4567e89b42d3a456426614174000"
	module := otelx.New("test", "1.0.0", otelx.WithIDGenerator(func() string { return traceID }))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	var output bytes.Buffer
	logger := slog.New(slogx.NewTraceHandler(slog.NewJSONHandler(&output, nil)))
	ctx, span := module.Tracer("test").Start(t.Context(), "operation")
	logger.InfoContext(ctx, "completed")
	span.End()

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, traceID, record["trace_id"])
	assert.Equal(t, otelx.SpanID(ctx), record["span_id"])
}
