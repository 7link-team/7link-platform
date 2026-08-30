package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/infra/health/service"
	humax "7link.com/platform/pkg/extension/huma"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestHealthModuleLifecycleAndRegistration(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(platformi18n.Base).Initialize(t.Context()))
	var m *HealthModule
	m = NewModule()
	services := module.NewServices()
	require.NoError(t, module.Provide[service.CheckerProvider](services, func() []service.Checker {
		return []service.Checker{{Name: m.ModuleName(), Check: m.CheckHealth}}
	}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	assert.Equal(t, "health", m.ModuleName())
	assert.Equal(t, []string{"i18n"}, m.ModuleDependencies())
	require.NotNil(t, m.Service())
	assert.ErrorIs(t, m.CheckHealth(t.Context()), ErrNotReady)
	assert.Equal(t, service.StatusNotServing, m.Service().Check(t.Context()).Status)
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, m.Start(t.Context()))
	assert.NoError(t, m.CheckHealth(t.Context()))

	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("health-test", "1.0.0"))
	require.NoError(t, m.RegisterHTTP(t.Context(), router, api))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Code int `json:"code"`
		Data struct {
			Status service.Status `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Zero(t, body.Code)
	assert.Equal(t, service.StatusServing, body.Data.Status)

	grpcServer := grpc.NewServer()
	m.RegisterGRPC(grpcServer)
	assert.Contains(t, grpcServer.GetServiceInfo(), "platform.health.v1.HealthService")

	require.NoError(t, m.Stop(t.Context()))
	assert.ErrorIs(t, m.CheckHealth(t.Context()), ErrNotReady)
	assert.Equal(t, service.StatusNotServing, m.Service().Check(t.Context()).Status)
}

func TestHealthModuleQuiesceDisablesReadiness(t *testing.T) {
	m := NewModule()
	require.NoError(t, m.Start(t.Context()))
	require.NoError(t, m.Quiesce(t.Context()))
	assert.ErrorIs(t, m.CheckHealth(t.Context()), ErrNotReady)
}
