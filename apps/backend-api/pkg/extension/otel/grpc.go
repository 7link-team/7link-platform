package otelx

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (m *Module) UnaryServerInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	m.setGRPCTraceHeaders(ctx, func(pairs metadata.MD) error {
		return grpc.SetHeader(ctx, pairs)
	})
	started := time.Now()
	response, err := handler(ctx, request)
	logGRPC(ctx, info.FullMethod, started, err)
	return response, err
}

func (m *Module) StreamServerInterceptor(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	ctx := stream.Context()
	m.setGRPCTraceHeaders(ctx, stream.SetHeader)
	started := time.Now()
	err := handler(service, stream)
	logGRPC(ctx, info.FullMethod, started, err)
	return err
}

func (m *Module) setGRPCTraceHeaders(ctx context.Context, set func(metadata.MD) error) {
	if TraceID(ctx) == "" {
		return
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceIDPropagator{}.Inject(ctx, carrier)
	values := metadata.MD{}
	for key, value := range carrier {
		values.Set(key, value)
	}
	_ = set(values)
}

func logGRPC(ctx context.Context, method string, started time.Time, err error) {
	slog.InfoContext(ctx, "gRPC request completed",
		"method", method,
		"code", status.Code(err).String(),
		"duration_ms", float64(time.Since(started).Nanoseconds())/1e6,
	)
}
