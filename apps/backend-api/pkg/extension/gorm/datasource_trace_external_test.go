package gormx_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	telemetry "7link.com/platform/internal/infra/telemetry"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	slogx "7link.com/platform/pkg/extension/slog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasourceSQLLogContainsTraceID(t *testing.T) {
	const traceID = "123e4567e89b42d3a456426614174000"
	telemetryModule := telemetry.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), telemetry.WithIDGenerator(func() string { return traceID }))
	require.NoError(t, telemetryModule.Initialize(t.Context()))
	t.Cleanup(func() { require.NoError(t, telemetryModule.Deinitialize(context.Background())) })

	previousLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slogx.NewTraceHandler(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))))

	router, err := gormx.NewDatasourceRouter(t.Context(), true, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "trace.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })

	ctx, span := telemetryModule.Tracer("test").Start(t.Context(), "query")
	require.NoError(t, router.Write().Debug().WithContext(ctx).Exec("SELECT 1").Error)
	span.End()

	assert.Contains(t, output.String(), `"trace_id":"`+traceID+`"`)
	assert.Contains(t, output.String(), `"sql":"SELECT 1"`)
}
