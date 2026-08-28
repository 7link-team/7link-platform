package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPprofServesStandardEndpoints(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, "test", "1.0.0")
	registerPprof(router, api, true)

	index := httptest.NewRecorder()
	router.ServeHTTP(index, httptest.NewRequest(http.MethodGet, pprofPath+"/", nil))
	assert.Equal(t, http.StatusOK, index.Code)
	assert.Contains(t, index.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, index.Body.String(), "Types of profiles available")

	goroutine := httptest.NewRecorder()
	router.ServeHTTP(goroutine, httptest.NewRequest(http.MethodGet, pprofPath+"/goroutine?debug=1", nil))
	assert.Equal(t, http.StatusOK, goroutine.Code)
	assert.Contains(t, goroutine.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, goroutine.Body.String(), "goroutine profile")

	redirect := httptest.NewRecorder()
	router.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, pprofPath, nil))
	assert.Equal(t, http.StatusMovedPermanently, redirect.Code)
	assert.Equal(t, pprofPath+"/", redirect.Header().Get("Location"))

	wrongMethod := httptest.NewRecorder()
	router.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, pprofPath+"/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, wrongMethod.Code)
}

func TestRegisterPprofAddsOpenAPIOperations(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, "test", "1.0.0")
	registerPprof(router, api, true)

	paths := api.OpenAPI().Paths
	for _, path := range []string{
		pprofPath + "/",
		pprofPath + "/cmdline",
		pprofPath + "/profile",
		pprofPath + "/symbol",
		pprofPath + "/trace",
		pprofPath + "/{profile}",
	} {
		require.Contains(t, paths, path)
		require.NotNil(t, paths[path].Get)
		assert.Contains(t, paths[path].Get.Tags, "PProf")
	}

	profile := paths[pprofPath+"/{profile}"].Get
	require.Len(t, profile.Parameters, 4)
	assert.Equal(t, "profile", profile.Parameters[0].Name)
	assert.True(t, profile.Parameters[0].Required)
	assert.Contains(t, profile.Responses["200"].Content, "application/octet-stream")
	assert.Contains(t, profile.Responses["200"].Content, "text/plain")

	_, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
}

func TestRegisterPprofDoesNothingWhenDisabled(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, "test", "1.0.0")
	registerPprof(router, api, false)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pprofPath+"/", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)

	for path := range api.OpenAPI().Paths {
		assert.NotContains(t, path, pprofPath)
	}
}
