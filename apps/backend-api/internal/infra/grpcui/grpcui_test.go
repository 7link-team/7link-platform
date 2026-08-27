package grpcui

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	healthgrpc "7link.com/platform/internal/infra/health/grpc"
	"7link.com/platform/internal/infra/health/service"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func TestModuleRegistersUIAndClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	healthgrpc.NewServer(service.New()).Register(server)
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	module := NewModule(listener.Addr().String())
	router := chi.NewRouter()
	require.NoError(t, module.RegisterHTTP(t.Context(), router, nil))
	require.NotNil(t, module.conn)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path+"/", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "platform.health.v1.HealthService")

	require.NoError(t, module.Stop(t.Context()))
	assert.Nil(t, module.conn)
}
