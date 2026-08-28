package grpcx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRecoveryUnaryInterceptor(t *testing.T) {
	response, err := RecoveryUnaryInterceptor(t.Context(), nil,
		&grpcgo.UnaryServerInfo{FullMethod: "/test.Service/Call"},
		func(context.Context, any) (any, error) { panic("failure") },
	)
	assert.Nil(t, response)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestRecoveryStreamInterceptor(t *testing.T) {
	err := RecoveryStreamInterceptor(nil, &localeServerStream{ctx: t.Context()},
		&grpcgo.StreamServerInfo{FullMethod: "/test.Service/Stream"},
		func(any, grpcgo.ServerStream) error { panic("failure") },
	)
	assert.Equal(t, codes.Internal, status.Code(err))
}
