package grpcx

import (
	"context"

	"7link.com/platform/pkg/i18n"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// LocaleUnaryInterceptor makes message localization consistent for every gRPC
// module. Clients select a locale with the accept-language metadata key.
func LocaleUnaryInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	locale := i18n.Base
	if values := metadata.ValueFromIncomingContext(ctx, "accept-language"); len(values) > 0 {
		locale = i18n.Canonical(values[0])
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs("content-language", locale))
	return handler(i18n.WithLocale(ctx, locale), req)
}

// LocaleStreamInterceptor provides the same locale propagation for server,
// client, and bidirectional streaming RPCs.
func LocaleStreamInterceptor(
	srv any,
	stream grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	ctx := stream.Context()
	locale := i18n.Base
	if values := metadata.ValueFromIncomingContext(ctx, "accept-language"); len(values) > 0 {
		locale = i18n.Canonical(values[0])
	}
	_ = stream.SetHeader(metadata.Pairs("content-language", locale))
	return handler(srv, &localizedServerStream{
		ServerStream: stream,
		ctx:          i18n.WithLocale(ctx, locale),
	})
}

type localizedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *localizedServerStream) Context() context.Context {
	return s.ctx
}
