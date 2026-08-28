package grpcx

import (
	"context"
	"testing"

	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type localeTransportStream struct{ header metadata.MD }

func (*localeTransportStream) Method() string { return "/test.Service/Method" }
func (s *localeTransportStream) SetHeader(header metadata.MD) error {
	s.header = metadata.Join(s.header, header)
	return nil
}
func (s *localeTransportStream) SendHeader(header metadata.MD) error {
	return s.SetHeader(header)
}
func (*localeTransportStream) SetTrailer(metadata.MD) error { return nil }

func TestLocaleUnaryInterceptor(t *testing.T) {
	transport := &localeTransportStream{}
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("accept-language", "zh-TW"))
	ctx = grpc.NewContextWithServerTransportStream(ctx, transport)

	response, err := LocaleUnaryInterceptor(ctx, "request", nil, func(ctx context.Context, request any) (any, error) {
		assert.Equal(t, "zh-CN", i18n.LocaleFromContext(ctx))
		assert.Equal(t, "request", request)
		return "response", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "response", response)
	assert.Equal(t, []string{"zh-CN"}, transport.header.Get("content-language"))
}

func TestLocaleUnaryInterceptorDefaultsToBase(t *testing.T) {
	_, err := LocaleUnaryInterceptor(t.Context(), nil, nil, func(ctx context.Context, _ any) (any, error) {
		assert.Equal(t, i18n.Base, i18n.LocaleFromContext(ctx))
		return nil, nil
	})
	require.NoError(t, err)
}

type localeServerStream struct {
	grpc.ServerStream
	ctx    context.Context
	header metadata.MD
}

func (s *localeServerStream) Context() context.Context { return s.ctx }
func (s *localeServerStream) SetHeader(header metadata.MD) error {
	s.header = metadata.Join(s.header, header)
	return nil
}

func TestLocaleStreamInterceptor(t *testing.T) {
	stream := &localeServerStream{
		ctx: metadata.NewIncomingContext(t.Context(), metadata.Pairs("accept-language", "ms")),
	}
	called := false
	err := LocaleStreamInterceptor("service", stream, nil, func(service any, localized grpc.ServerStream) error {
		called = true
		assert.Equal(t, "service", service)
		assert.Equal(t, "ms-MY", i18n.LocaleFromContext(localized.Context()))
		return nil
	})

	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, []string{"ms-MY"}, stream.header.Get("content-language"))
}
