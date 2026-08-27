package guid

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	guidv1 "7link.com/platform/internal/infra/guid/proto/gen/go/guid/v1"
	grpcx "7link.com/platform/pkg/extension/grpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func grpcCounter() func() (int64, error) {
	var value atomic.Int64
	return func() (int64, error) { return value.Add(1), nil }
}

func newGRPCClient(t *testing.T, next func() (int64, error)) guidv1.GuidServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(grpcx.ValidationUnaryInterceptor))
	guidv1.RegisterGuidServiceServer(server, &grpcServer{next: next})
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return guidv1.NewGuidServiceClient(connection)
}

func TestGRPCEndpoints(t *testing.T) {
	client := newGRPCClient(t, grpcCounter())
	one, err := client.Next(context.Background(), &guidv1.NextRequest{})
	require.NoError(t, err)
	assert.Equal(t, "1", one.GetId())

	batch, err := client.NextBatch(context.Background(), &guidv1.NextBatchRequest{Count: 3})
	require.NoError(t, err)
	assert.Equal(t, []string{"2", "3", "4"}, batch.GetIds())

	batch, err = client.NextBatch(context.Background(), &guidv1.NextBatchRequest{Count: 10000})
	require.NoError(t, err)
	assert.Len(t, batch.GetIds(), 10000)

	_, err = client.NextBatch(context.Background(), &guidv1.NextBatchRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.NextBatch(context.Background(), &guidv1.NextBatchRequest{Count: 10001})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
