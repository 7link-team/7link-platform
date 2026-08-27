package grpcx

import (
	"context"
	"testing"

	guidv1 "7link.com/platform/internal/infra/guid/proto/gen/go/guid/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestValidationUnaryInterceptor(t *testing.T) {
	called := false
	handler := func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	}

	_, err := ValidationUnaryInterceptor(
		context.Background(),
		&guidv1.NextBatchRequest{},
		nil,
		handler,
	)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, called)

	_, err = ValidationUnaryInterceptor(
		context.Background(),
		&guidv1.NextBatchRequest{Count: 1},
		nil,
		handler,
	)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestValidationStreamInterceptor(t *testing.T) {
	stream := &validationTestServerStream{
		message: &guidv1.NextBatchRequest{},
	}
	err := ValidationStreamInterceptor(nil, stream, nil, func(_ any, stream grpc.ServerStream) error {
		return stream.RecvMsg(&guidv1.NextBatchRequest{})
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

type validationTestServerStream struct {
	grpc.ServerStream
	message proto.Message
}

func (stream *validationTestServerStream) Context() context.Context {
	return context.Background()
}

func (stream *validationTestServerStream) RecvMsg(message any) error {
	proto.Merge(message.(proto.Message), stream.message)
	return nil
}
