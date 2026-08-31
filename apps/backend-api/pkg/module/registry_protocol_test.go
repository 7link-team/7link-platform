package module

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

type protocolModule struct {
	namedModule
	httpCalled bool
	httpErr    error
	grpcCalled bool
}

func (m *protocolModule) RegisterHTTP(context.Context, huma.API) error {
	m.httpCalled = true
	return m.httpErr
}

func (m *protocolModule) RegisterGRPC(grpc.ServiceRegistrar) { m.grpcCalled = true }

func (*protocolModule) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Middleware", "protocol")
		next.ServeHTTP(w, r)
	})
}

func (*protocolModule) RawHTTPRoutes() []RawRoute {
	return []RawRoute{
		{Method: http.MethodGet, Path: "/only-get", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})},
		{Path: "/any", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})},
	}
}

func (*protocolModule) GRPCServerStatsHandler() stats.Handler { return protocolStatsHandler{} }

func (*protocolModule) UnaryServerInterceptor(
	ctx context.Context,
	request any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	return handler(ctx, request)
}

func (*protocolModule) StreamServerInterceptor(
	service any,
	stream grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	return handler(service, stream)
}

type protocolStatsHandler struct{}

func (protocolStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (protocolStatsHandler) HandleRPC(context.Context, stats.RPCStats) {}
func (protocolStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (protocolStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

func startedRegistry(t *testing.T, modules ...Module) *Registry {
	t.Helper()
	registry := NewRegistry()
	require.NoError(t, registry.Register(modules...))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	return registry
}

func TestRegistryProtocolCapabilities(t *testing.T) {
	capabilities := &protocolModule{namedModule: namedModule{name: "protocol"}}
	registry := startedRegistry(t, &namedModule{name: "plain"}, capabilities)

	require.NoError(t, registry.RegisterHTTP(t.Context(), nil))
	require.NoError(t, registry.RegisterGRPC(nil))
	assert.True(t, capabilities.httpCalled)
	assert.True(t, capabilities.grpcCalled)
	assert.NotNil(t, registry.Services())
	assert.True(t, registry.Services().sealed)

	middlewares := registry.HTTPMiddlewares()
	require.Len(t, middlewares, 1)
	recorder := httptest.NewRecorder()
	middlewares[0](http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "protocol", recorder.Header().Get("X-Middleware"))

	assert.Len(t, registry.GRPCProtocolOptions(), 3, "one stats handler plus unary and stream chains")
	routes := registry.RawHTTPRoutes()
	require.Len(t, routes, 2)
	assert.Equal(t, http.MethodGet, routes[0].Method)
	assert.Equal(t, "/only-get", routes[0].Path)
	assert.Empty(t, routes[1].Method)
	assert.Equal(t, "/any", routes[1].Path)
	getResponse := httptest.NewRecorder()
	routes[0].Handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/only-get", nil))
	assert.Equal(t, http.StatusNoContent, getResponse.Code)
	postResponse := httptest.NewRecorder()
	routes[1].Handler.ServeHTTP(postResponse, httptest.NewRequest(http.MethodPost, "/any", nil))
	assert.Equal(t, http.StatusAccepted, postResponse.Code)

	require.NoError(t, registry.Stop(t.Context()))
	assert.ErrorContains(t, registry.RegisterHTTP(t.Context(), nil), "requires started")
	assert.ErrorContains(t, registry.RegisterGRPC(nil), "requires started")
}

func TestRegistryRegisterHTTPWrapsModuleError(t *testing.T) {
	want := errStringModule("register failed")
	capabilities := &protocolModule{namedModule: namedModule{name: "protocol"}, httpErr: want}
	registry := startedRegistry(t, capabilities)
	err := registry.RegisterHTTP(t.Context(), nil)
	assert.ErrorIs(t, err, want)
	assert.ErrorContains(t, err, "register HTTP")
}

type errStringModule string

func (err errStringModule) Error() string { return string(err) }
