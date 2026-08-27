package grpcx

import (
	"context"
	"errors"
	"log/slog"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ValidationUnaryInterceptor applies validation rules declared in protobuf
// descriptors before dispatching unary requests to a service implementation.
func ValidationUnaryInterceptor(
	ctx context.Context,
	request any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if err := validateRequest(ctx, request); err != nil {
		return nil, err
	}
	return handler(ctx, request)
}

// ValidationStreamInterceptor validates every message received from a client.
func ValidationStreamInterceptor(
	server any,
	stream grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	return handler(server, &validatingServerStream{ServerStream: stream})
}

type validatingServerStream struct {
	grpc.ServerStream
}

func (stream *validatingServerStream) RecvMsg(message any) error {
	if err := stream.ServerStream.RecvMsg(message); err != nil {
		return err
	}
	return validateRequest(stream.Context(), message)
}

func validateRequest(ctx context.Context, request any) error {
	message, ok := request.(proto.Message)
	if !ok {
		return nil
	}
	if err := protovalidate.Validate(message); err != nil {
		var validationErr *protovalidate.ValidationError
		if errors.As(err, &validationErr) {
			return status.Error(codes.InvalidArgument, validationErr.Error())
		}
		slog.ErrorContext(ctx, "protobuf request validation failed", "error", err)
		return status.Error(codes.Internal, "request validation failed")
	}
	return nil
}
