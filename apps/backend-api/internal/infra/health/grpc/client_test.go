package grpc

import (
	"context"
	"net"
	"strings"
	"testing"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	telemetry "7link.com/platform/internal/infra/telemetry"
	grpcx "7link.com/platform/pkg/extension/grpc"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestClientCheckEndToEnd(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, healthi18n.Register())
	const traceID = "123e4567e89b42d3a456426614174000"
	telemetryModule := telemetry.NewModule(module.Runtime{Name: "health-test", Version: "1.0.0"}, telemetry.WithIDGenerator(func() string { return traceID }))
	t.Cleanup(func() { require.NoError(t, telemetryModule.Deinitialize(context.Background())) })

	listener := bufconn.Listen(1024 * 1024)
	server := grpcgo.NewServer(
		grpcgo.StatsHandler(telemetryModule.GRPCServerStatsHandler()),
		grpcgo.ChainUnaryInterceptor(telemetryModule.UnaryServerInterceptor, grpcx.LocaleUnaryInterceptor),
	)
	NewServer(service.New(func() []service.Checker {
		return []service.Checker{{Name: "health"}}
	})).Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	conn, err := grpcgo.NewClient(
		"passthrough:///health-test",
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpcgo.WithStatsHandler(telemetryModule.GRPCClientStatsHandler()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	ctx := platformi18n.WithLocale(t.Context(), "zh-CN")
	var headers metadata.MD
	result, err := NewClient(conn).Check(ctx, grpcgo.Header(&headers))
	require.NoError(t, err)
	assert.Equal(t, 0, result.Code)
	assert.Equal(t, "健康检查已完成。", result.Message)
	assert.Equal(t, service.StatusServing, result.Data.Status)
	assert.Equal(t, []service.ModuleStatus{{Name: "health", Status: service.StatusUnknown}}, result.Data.Modules)
	assert.Equal(t, []string{"zh-CN"}, headers.Get("content-language"))
	assert.Equal(t, []string{traceID}, headers.Get(strings.ToLower(telemetry.TraceIDHeader)))
	require.Len(t, headers.Get(telemetry.TraceparentHeader), 1)
	assert.Contains(t, headers.Get(telemetry.TraceparentHeader)[0], traceID)
}
