package audit

import (
	"context"
	"net"
	"testing"
	"time"

	"7link.com/platform/internal/infra/telemetry"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func grpcModuleConfig() Config {
	return Config{
		Enabled:       true,
		BatchSize:     1,
		FlushInterval: time.Millisecond,
		FlushTimeout:  time.Second,
		MaxBodyBytes:  256,
		QueueSize:     1024,
	}
}

func newStructRequest(t *testing.T) *structpb.Struct {
	t.Helper()
	request, err := structpb.NewStruct(map[string]any{"name": "alice", "password": "secret"})
	require.NoError(t, err)
	return request
}

func TestUnaryServerInterceptorSkipsReadsByDefault(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/GetOrder"}
	_, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(_ context.Context, _ any) (any, error) {
		return newStructRequest(t), nil
	})
	require.NoError(t, err)
	assert.Empty(t, sink.snapshot())
}

func TestUnaryServerInterceptorRecordsWrite(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/CreateOrder"}
	response, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(ctx context.Context, _ any) (any, error) {
		Annotate(ctx, Action("order.create"), Resource("order", "77"))
		return newStructRequest(t), nil
	})
	require.NoError(t, err)
	require.NotNil(t, response)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindGRPC, event.Entry)
	assert.Equal(t, "order.create", event.Action)
	assert.Equal(t, "77", event.ResourceID)
	assert.Equal(t, OutcomeSuccess, event.Outcome)
	assert.Equal(t, int(codes.OK), event.StatusCode)
	// Request body is protojson, redacted.
	assert.Contains(t, event.Request.Body, "alice")
	assert.NotContains(t, event.Request.Body, "secret")
	assert.Contains(t, event.Request.Body, "[REDACTED]")
	// Success response is summary-only for gRPC.
	assert.Equal(t, OmittedSuccessSummary, event.Response.Omitted)
}

func TestUnaryServerInterceptorRecordsFailure(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/UpdateOrder"}
	_, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.InvalidArgument, "bad payload")
	})
	require.Error(t, err)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OutcomeFailure, event.Outcome)
	assert.Equal(t, int(codes.InvalidArgument), event.StatusCode)
	assert.Contains(t, event.Error, "bad payload")
	assert.Contains(t, event.Request.Body, "[REDACTED]")
}

func TestUnaryServerInterceptorPromotesReadViaAnnotate(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/GetOrder"}
	_, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(ctx context.Context, _ any) (any, error) {
		Annotate(ctx, SetAccess(AccessWrite))
		return newStructRequest(t), nil
	})
	require.NoError(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, AccessWrite, sink.snapshot()[0].Access)
}

func TestUnaryServerInterceptorDisabled(t *testing.T) {
	config := grpcModuleConfig()
	config.Enabled = false
	module, sink := newTestModule(t, config)
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/CreateOrder"}
	_, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(_ context.Context, _ any) (any, error) {
		return newStructRequest(t), nil
	})
	require.NoError(t, err)
	assert.Empty(t, sink.snapshot())
}

func TestUnaryServerInterceptorCapsOversizedRequest(t *testing.T) {
	config := grpcModuleConfig()
	config.MaxBodyBytes = 8
	module, sink := newTestModule(t, config)
	info := &grpc.UnaryServerInfo{FullMethod: "/order.v1.OrderService/CreateOrder"}
	_, err := module.UnaryServerInterceptor(t.Context(), newStructRequest(t), info, func(_ context.Context, _ any) (any, error) {
		return newStructRequest(t), nil
	})
	require.NoError(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OmittedTooLarge, event.Request.Omitted)
	// The oversized request is never marshaled: no body, only the size.
	assert.Empty(t, event.Request.Body)
	assert.True(t, event.Request.Truncated)
	assert.Greater(t, event.Request.Size, int64(0))
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *fakeServerStream) Context() context.Context { return stream.ctx }

func TestStreamServerInterceptorRecordsSession(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.StreamServerInfo{FullMethod: "/order.v1.OrderService/TrackOrder"}
	stream := &fakeServerStream{ctx: t.Context()}
	err := module.StreamServerInterceptor(nil, stream, info, func(_ any, stream grpc.ServerStream) error {
		Annotate(stream.Context(), Action("order.track"))
		return nil
	})
	require.NoError(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindGRPC, event.Entry)
	assert.Equal(t, "order.track", event.Action)
	assert.Equal(t, "stream", event.Target.Method)
	assert.Equal(t, OutcomeSuccess, event.Outcome)
}

func TestStreamServerInterceptorFailure(t *testing.T) {
	module, sink := newTestModule(t, grpcModuleConfig())
	info := &grpc.StreamServerInfo{FullMethod: "/order.v1.OrderService/TrackOrder"}
	stream := &fakeServerStream{ctx: t.Context()}
	err := module.StreamServerInterceptor(nil, stream, info, func(any, grpc.ServerStream) error {
		return status.Error(codes.Aborted, "stream aborted")
	})
	require.Error(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, OutcomeFailure, sink.snapshot()[0].Outcome)
}

// echoService is a minimal unary gRPC service used by the trace-linkage test.
type echoService struct{}

func (echoService) Echo(_ context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	return request, nil
}

// TestGRPCAuditEventCarriesTraceIDs pins the gRPC trace linkage: with the
// telemetry stats handler registered, otelgrpc's TagRPC injects the call's span
// context into the interceptor context, so the audit event must carry the
// client's trace ID. This guards against silently losing trace linkage if the
// otelgrpc behavior changes.
func TestGRPCAuditEventCarriesTraceIDs(t *testing.T) {
	auditModule, sink := newTestModule(t, grpcModuleConfig())
	telemetryModule := telemetry.NewModule(module.Runtime{Name: "test-api", Version: "1.0.0"})
	t.Cleanup(func() { _ = telemetryModule.Deinitialize(t.Context()) })

	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(
		grpc.StatsHandler(telemetryModule.GRPCServerStatsHandler()),
		grpc.ChainUnaryInterceptor(auditModule.UnaryServerInterceptor),
	)
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "audit.test.v1.Echo",
		HandlerType: (*interface {
			Echo(context.Context, *structpb.Struct) (*structpb.Struct, error)
		})(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Echo",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := &structpb.Struct{}
				if err := dec(request); err != nil {
					return nil, err
				}
				info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/audit.test.v1.Echo/Echo"}
				handler := func(ctx context.Context, value any) (any, error) {
					return srv.(echoService).Echo(ctx, value.(*structpb.Struct))
				}
				if interceptor == nil {
					return handler(ctx, request)
				}
				return interceptor(ctx, request, info, handler)
			},
		}},
	}, echoService{})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	ctx := metadata.NewOutgoingContext(t.Context(),
		metadata.Pairs("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01"))
	response := &structpb.Struct{}
	require.NoError(t, conn.Invoke(ctx, "/audit.test.v1.Echo/Echo", &structpb.Struct{}, response))

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindGRPC, event.Entry)
	assert.Equal(t, traceID, event.TraceID)
	assert.NotEmpty(t, event.SpanID)
}

func TestServerStreamContextPropagates(t *testing.T) {
	module, _ := newTestModule(t, grpcModuleConfig())
	info := &grpc.StreamServerInfo{FullMethod: "/order.v1.OrderService/TrackOrder"}
	stream := &fakeServerStream{ctx: t.Context()}
	_ = module.StreamServerInterceptor(nil, stream, info, func(_ any, stream grpc.ServerStream) error {
		entry, ok := inflightFromContext(stream.Context())
		require.True(t, ok)
		assert.NotNil(t, entry.event)
		return nil
	})
}
