package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/infra/health/service"
	humax "7link.com/platform/pkg/extension/huma"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestHealthModuleLifecycleAndRegistration(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(platformi18n.Base).Initialize(t.Context()))
	var module *HealthModule
	module = NewModule(func() []service.Checker {
		return []service.Checker{{Name: module.ModuleName(), Check: module.CheckHealth}}
	})
	assert.Equal(t, "health", module.ModuleName())
	assert.Equal(t, []string{"i18n"}, module.ModuleDependencies())
	require.NotNil(t, module.Service())
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrNotReady)
	assert.Equal(t, service.StatusNotServing, module.Service().Check(t.Context()).Status)
	require.NoError(t, module.Initialize(t.Context()))
	require.NoError(t, module.Start(t.Context()))
	assert.NoError(t, module.CheckHealth(t.Context()))

	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("health-test", "1.0.0"))
	require.NoError(t, module.RegisterHTTP(t.Context(), router, api))
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
	module.RegisterGRPC(grpcServer)
	assert.Contains(t, grpcServer.GetServiceInfo(), "platform.health.v1.HealthService")

	require.NoError(t, module.Stop(t.Context()))
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrNotReady)
	assert.Equal(t, service.StatusNotServing, module.Service().Check(t.Context()).Status)
}

func TestHealthModuleQuiesceDisablesReadiness(t *testing.T) {
	module := NewModule()
	require.NoError(t, module.Start(t.Context()))
	require.NoError(t, module.Quiesce(t.Context()))
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrNotReady)
}
