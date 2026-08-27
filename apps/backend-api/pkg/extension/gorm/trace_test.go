package gormx

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	otelx "7link.com/platform/pkg/extension/otel"
	slogx "7link.com/platform/pkg/extension/slog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLLogContainsTraceID(t *testing.T) {
	const traceID = "123e4567e89b42d3a456426614174000"
	module := otelx.New("test", "1.0.0", otelx.WithIDGenerator(func() string { return traceID }))
	t.Cleanup(func() { require.NoError(t, module.Deinitialize(context.Background())) })

	previousLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slogx.NewTraceHandler(slog.NewJSONHandler(&output, nil))))

	db, err := (Datasource{
		Type: "sqlite",
		DSN:  filepath.Join(t.TempDir(), "trace.sqlite"),
	}).open(t.Context(), true)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx, span := module.Tracer("test").Start(t.Context(), "query")
	require.NoError(t, db.WithContext(ctx).Exec("SELECT 1").Error)
	span.End()

	assert.Contains(t, output.String(), `"trace_id":"`+traceID+`"`)
	assert.Contains(t, output.String(), `"sql":"SELECT 1"`)
}
