package slogx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	telemetry "7link.com/platform/internal/infra/telemetry"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceHandlerAddsTraceAndSpanIDs(t *testing.T) {
	const traceID = "123e4567e89b42d3a456426614174000"
	module := telemetry.NewModule(module.Runtime{Name: "test", Version: "1.0.0"}, telemetry.WithIDGenerator(func() string { return traceID }))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	var output bytes.Buffer
	logger := slog.New(slogx.NewTraceHandler(slog.NewJSONHandler(&output, nil)))
	ctx, span := module.Tracer("test").Start(t.Context(), "operation")
	logger.InfoContext(ctx, "completed")
	span.End()

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, traceID, record["trace_id"])
	assert.Equal(t, telemetry.SpanID(ctx), record["span_id"])
}

func TestConsoleUsesColorsOnlyInDebugMode(t *testing.T) {
	for _, test := range []struct {
		name  string
		debug bool
		color bool
	}{
		{name: "normal", debug: false, color: false},
		{name: "debug", debug: true, color: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slogx.NewConsole(&output, test.debug, "test-api", nil)
			logger.Info("server ready",
				"method", "GET",
				"status", 201,
				"port", 8080,
				"healthy", true,
				"error", errors.New("connection reset"),
			)

			assert.Equal(t, test.color, bytes.Contains(output.Bytes(), []byte("\x1b[")))
			assert.Contains(t, output.String(), "server ready")
			assert.Contains(t, output.String(), "port=")
			assert.Contains(t, output.String(), "8080")
		})
	}
}

func TestDebugConsoleUsesPatternLayout(t *testing.T) {
	var output bytes.Buffer
	logger := slogx.NewConsole(&output, true, "test-api", nil)
	logger.Info("request completed",
		"method", "GET",
		"status", 201,
		"duration_ms", 12.5,
		"healthy", true,
		"error", errors.New("connection reset"),
	)
	logger.Debug("debug details")

	assert.Contains(t, output.String(), "\x1b[93m20")
	assert.Contains(t, output.String(), "\x1b[92mINFO \x1b[0m")
	assert.Contains(t, output.String(), "\x1b[96mDEBUG\x1b[0m")
	assert.Contains(t, output.String(), "\x1b[22;1;95m[test-api]\x1b[0m")
	assert.Contains(t, output.String(), "\x1b[36m(trace_test.go:")
	assert.Contains(t, output.String(), "\x1b[2m-->*\x1b[0m")
	assert.Contains(t, output.String(), "request completed")
	assert.Contains(t, output.String(), "\x1b[2m\x1b[22mmethod=\x1b[0mGET")
	assert.Contains(t, output.String(), "\x1b[2;91m\x1b[22merror=\x1b[22m\"connection reset\"\x1b[0m")
}

func TestDebugConsoleFallsBackToExecutableName(t *testing.T) {
	var output bytes.Buffer
	slogx.NewConsole(&output, true, "", nil).Info("ready")

	assert.Contains(t, output.String(), "["+filepath.Base(os.Args[0])+"]")
}
