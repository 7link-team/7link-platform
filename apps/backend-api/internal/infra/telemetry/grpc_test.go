package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grpcTransportStream struct{ header metadata.MD }

func (*grpcTransportStream) Method() string { return "/test.Service/Unary" }
func (s *grpcTransportStream) SetHeader(header metadata.MD) error {
	s.header = metadata.Join(s.header, header)
	return nil
}
func (s *grpcTransportStream) SendHeader(header metadata.MD) error {
	return s.SetHeader(header)
}
func (*grpcTransportStream) SetTrailer(metadata.MD) error { return nil }

func TestUnaryServerInterceptorReturnsTraceHeadersAndHandlerError(t *testing.T) {
	module := newInitializedModule(t, WithIDGenerator(func() string { return generatedUUID }))
	ctx, span := module.Tracer("test").Start(t.Context(), "unary")
	defer span.End()
	transport := &grpcTransportStream{}
	ctx = grpc.NewContextWithServerTransportStream(ctx, transport)
	wantErr := status.Error(codes.Unavailable, "offline")

	response, err := module.UnaryServerInterceptor(
		ctx,
		"request",
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Unary"},
		func(_ context.Context, request any) (any, error) {
			assert.Equal(t, "request", request)
			return "response", wantErr
		},
	)

	assert.Equal(t, "response", response)
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, generatedHex, transport.header.Get(TraceIDHeader)[0])
	assert.Contains(t, transport.header.Get(TraceparentHeader)[0], generatedHex)
}

type grpcTestServerStream struct {
	grpc.ServerStream
	ctx    context.Context
	header metadata.MD
}

func (s *grpcTestServerStream) Context() context.Context { return s.ctx }
func (s *grpcTestServerStream) SetHeader(header metadata.MD) error {
	s.header = metadata.Join(s.header, header)
	return nil
}

func TestStreamServerInterceptorReturnsTraceHeaders(t *testing.T) {
	module := newInitializedModule(t, WithIDGenerator(func() string { return generatedUUID }))
	ctx, span := module.Tracer("test").Start(t.Context(), "stream")
	defer span.End()
	stream := &grpcTestServerStream{ctx: ctx}

	err := module.StreamServerInterceptor(
		"service",
		stream,
		&grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"},
		func(service any, got grpc.ServerStream) error {
			assert.Equal(t, "service", service)
			assert.Same(t, stream, got)
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, generatedHex, stream.header.Get(TraceIDHeader)[0])
	assert.NotEmpty(t, SpanID(ctx))
}

func TestSetGRPCTraceHeadersSkipsContextWithoutSpan(t *testing.T) {
	module := newInitializedModule(t)
	called := false
	module.setGRPCTraceHeaders(t.Context(), func(metadata.MD) error {
		called = true
		return nil
	})
	assert.False(t, called)
}
