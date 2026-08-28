package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func RecoveryUnaryInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (response any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logRecoveredPanic(ctx, info.FullMethod, recovered)
			response = nil
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, request)
}

func RecoveryStreamInterceptor(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logRecoveredPanic(stream.Context(), info.FullMethod, recovered)
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(service, stream)
}

func logRecoveredPanic(ctx context.Context, method string, recovered any) {
	slog.ErrorContext(ctx, "gRPC handler panic recovered",
		"method", method,
		"panic", recovered,
		"stack", string(debug.Stack()),
	)
}
