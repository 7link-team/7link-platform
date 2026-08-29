package pprof

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPprofServesStandardEndpoints(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	module := NewModule("test service")
	assert.Equal(t, "pprof", module.ModuleName())
	require.NoError(t, module.RegisterHTTP(t.Context(), router, api))

	index := httptest.NewRecorder()
	router.ServeHTTP(index, httptest.NewRequest(http.MethodGet, Path+"/", nil))
	assert.Equal(t, http.StatusOK, index.Code)
	assert.Contains(t, index.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, index.Body.String(), "Types of profiles available")

	goroutine := httptest.NewRecorder()
	router.ServeHTTP(goroutine, httptest.NewRequest(http.MethodGet, Path+"/goroutine?debug=1", nil))
	assert.Equal(t, http.StatusOK, goroutine.Code)
	assert.Contains(t, goroutine.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, goroutine.Body.String(), "goroutine profile")

	redirect := httptest.NewRecorder()
	router.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, Path, nil))
	assert.Equal(t, http.StatusMovedPermanently, redirect.Code)
	assert.Equal(t, Path+"/", redirect.Header().Get("Location"))

	wrongMethod := httptest.NewRecorder()
	router.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, Path+"/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, wrongMethod.Code)
}

func TestRegisterPprofServesEmbeddedWebUI(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	require.NoError(t, NewModule("test service").RegisterHTTP(t.Context(), router, api))

	landing := httptest.NewRecorder()
	router.ServeHTTP(landing, httptest.NewRequest(http.MethodGet, UIPath+"/", nil))
	require.Equal(t, http.StatusOK, landing.Code)
	assert.Contains(t, landing.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "no-store", landing.Header().Get("Cache-Control"))
	assert.Contains(t, landing.Body.String(), "Runtime profiles")
	assert.Contains(t, landing.Body.String(), `<a class="brand" href="/">test service</a>`)
	assert.Contains(t, landing.Body.String(), `action="/debug/pprof/ui/capture"`)
	assert.Contains(t, landing.Body.String(), `type="hidden" name="profile" value="heap"`)
	assert.Equal(t, len(pprofProfiles), strings.Count(landing.Body.String(), `<button class="capture-button"`))
	assert.NotContains(t, landing.Body.String(), `type="radio"`)
	assert.NotContains(t, landing.Body.String(), "<select")
	assert.NotContains(t, landing.Body.String(), "Duration")
	assert.Equal(t, 1, strings.Count(landing.Body.String(), `name="seconds" type="number"`))
	assert.Contains(t, landing.Body.String(), `min="1" max="30" value="5"`)
	assert.Contains(t, landing.Body.String(), "CPU capture duration in seconds")

	withoutCapture := httptest.NewRecorder()
	router.ServeHTTP(withoutCapture, httptest.NewRequest(http.MethodGet, UIPath+"/view/flamegraph", nil))
	assert.Equal(t, http.StatusSeeOther, withoutCapture.Code)
	assert.Equal(t, UIPath+"/", withoutCapture.Header().Get("Location"))

	form := url.Values{"profile": {"heap"}}
	capture := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, UIPath+"/capture", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(capture, request)
	require.Equal(t, http.StatusSeeOther, capture.Code, capture.Body.String())
	assert.Equal(t, UIPath+"/view/flamegraph", capture.Header().Get("Location"))

	flamegraph := httptest.NewRecorder()
	router.ServeHTTP(flamegraph, httptest.NewRequest(http.MethodGet, capture.Header().Get("Location"), nil))
	require.Equal(t, http.StatusOK, flamegraph.Code, flamegraph.Body.String())
	assert.Contains(t, flamegraph.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "no-store", flamegraph.Header().Get("Cache-Control"))
	assert.Contains(t, flamegraph.Body.String(), "pprof")
	assert.Contains(t, flamegraph.Body.String(), "Flame Graph")

	current := httptest.NewRecorder()
	router.ServeHTTP(current, httptest.NewRequest(http.MethodGet, UIPath+"/", nil))
	require.Equal(t, http.StatusOK, current.Code)
	assert.Contains(t, current.Body.String(), `<strong class="snapshot-name">Heap</strong>`)
	assert.Contains(t, current.Body.String(), `href="/debug/pprof/ui/view/flamegraph"`)

	download := httptest.NewRecorder()
	router.ServeHTTP(download, httptest.NewRequest(http.MethodGet, UIPath+"/view/download", nil))
	require.Equal(t, http.StatusOK, download.Code)
	assert.Equal(t, "application/vnd.google.protobuf+gzip", download.Header().Get("Content-Type"))
}

func TestPprofUIRejectsInvalidCapture(t *testing.T) {
	ui := newPprofUI("test service")
	for name, form := range map[string]url.Values{
		"profile": {"profile": {"unknown"}},
		"seconds": {"profile": {"cpu"}, "seconds": {"31"}},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/capture", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			ui.ServeHTTP(response, request)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Header().Get("Content-Type"), "text/html")
		})
	}
}

func TestPprofUIUsesRequestedCPUDuration(t *testing.T) {
	ui := newPprofUI("test service")
	var capturedDuration time.Duration
	ui.capture = func(_ context.Context, name string, duration time.Duration) (*profile.Profile, error) {
		assert.Equal(t, "cpu", name)
		capturedDuration = duration
		return nil, errors.New("stop after duration check")
	}

	form := url.Values{"profile": {"cpu"}, "seconds": {"7"}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/capture", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ui.ServeHTTP(response, request)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, 7*time.Second, capturedDuration)
}

func TestPprofUIRejectsConcurrentCapture(t *testing.T) {
	ui := newPprofUI("test service")
	started := make(chan struct{})
	release := make(chan struct{})
	ui.capture = func(context.Context, string, time.Duration) (*profile.Profile, error) {
		close(started)
		<-release
		return nil, errors.New("stopped")
	}

	form := url.Values{"profile": {"heap"}}
	first := httptest.NewRecorder()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/capture", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		ui.ServeHTTP(first, request())
	}()
	<-started

	second := httptest.NewRecorder()
	ui.ServeHTTP(second, request())
	assert.Equal(t, http.StatusConflict, second.Code)
	assert.Contains(t, second.Body.String(), "already running")
	close(release)
	wait.Wait()
	assert.Equal(t, http.StatusInternalServerError, first.Code)
}

func TestCaptureDuration(t *testing.T) {
	duration, err := captureDuration("cpu", "3")
	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, duration)

	duration, err = captureDuration("heap", "invalid")
	require.NoError(t, err)
	assert.Zero(t, duration)

	for _, value := range []string{"", "0", "31", "invalid"} {
		_, err = captureDuration("cpu", value)
		assert.Error(t, err)
	}
}

func TestRegisterPprofAddsOpenAPIOperations(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	require.NoError(t, NewModule("test service").RegisterHTTP(t.Context(), router, api))

	paths := api.OpenAPI().Paths
	for _, path := range []string{
		Path + "/",
		Path + "/cmdline",
		Path + "/profile",
		Path + "/symbol",
		Path + "/trace",
		Path + "/{profile}",
		UIPath + "/",
	} {
		require.Contains(t, paths, path)
		require.NotNil(t, paths[path].Get)
		assert.Equal(t, []string{"Diagnostics"}, paths[path].Get.Tags)
	}

	profile := paths[Path+"/{profile}"].Get
	require.Len(t, profile.Parameters, 4)
	assert.Equal(t, "profile", profile.Parameters[0].Name)
	assert.True(t, profile.Parameters[0].Required)
	assert.Contains(t, profile.Responses["200"].Content, "application/octet-stream")
	assert.Contains(t, profile.Responses["200"].Content, "text/plain")

	_, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
}
