package guid

import (
	"context"
	"sync/atomic"
	"testing"

	guidv1 "7link.com/platform/internal/infra/guid/proto/gen/go/guid/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func grpcCounter() func() (int64, error) {
	var value atomic.Int64
	return func() (int64, error) { return value.Add(1), nil }
}

func TestGRPCEndpoints(t *testing.T) {
	server := &grpcServer{next: grpcCounter()}
	one, err := server.Next(context.Background(), &guidv1.NextRequest{})
	require.NoError(t, err)
	assert.Equal(t, "1", one.GetId())

	batch, err := server.NextBatch(context.Background(), &guidv1.NextBatchRequest{Count: 3})
	require.NoError(t, err)
	assert.Equal(t, []string{"2", "3", "4"}, batch.GetIds())

	_, err = server.NextBatch(context.Background(), &guidv1.NextBatchRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.NextBatch(context.Background(), &guidv1.NextBatchRequest{Count: maxBatch + 1})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
