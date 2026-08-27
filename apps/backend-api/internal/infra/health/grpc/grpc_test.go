package grpc

import (
	"context"
	"net"
	"testing"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	grpcx "7link.com/platform/pkg/extension/grpc"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestServerAndClient(t *testing.T) {
	require.NoError(t, i18n.InitEmbedded(i18n.Base))
	require.NoError(t, healthi18n.Register())

	listener := bufconn.Listen(1024 * 1024)
	server := grpcgo.NewServer(grpcgo.UnaryInterceptor(grpcx.LocaleUnaryInterceptor))
	NewServer(service.New()).Register(server)
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
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	ctx := i18n.WithLocale(t.Context(), "zh-CN")
	var headers metadata.MD
	result, err := NewClient(conn).Check(ctx, grpcgo.Header(&headers))
	require.NoError(t, err)
	assert.Equal(t, 0, result.Code)
	assert.Equal(t, "健康检查成功完成。", result.Message)
	assert.Equal(t, service.StatusServing, result.Data.Status)
	assert.Equal(t, []string{"zh-CN"}, headers.Get("content-language"))
}
