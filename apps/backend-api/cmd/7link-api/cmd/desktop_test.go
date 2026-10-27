package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesktopListenerRetainsOriginAndRefusesConcurrentUse(t *testing.T) {
	directory := t.TempDir()
	first, err := desktopListener(directory)
	require.NoError(t, err)
	address := first.Addr().String()
	assert.Equal(t, "127.0.0.1", first.Addr().(*net.TCPAddr).IP.String())
	_, err = desktopListener(directory)
	assert.ErrorContains(t, err, "occupied")
	require.NoError(t, first.Close())
	second, err := desktopListener(directory)
	require.NoError(t, err)
	assert.Equal(t, address, second.Addr().String())
	require.NoError(t, second.Close())
	require.NoError(t, os.WriteFile(filepath.Join(directory, "http-port"), []byte("0"), 0o600))
	_, err = desktopListener(directory)
	assert.ErrorContains(t, err, "invalid persisted")
}

func TestDesktopHandlerKeepsWebAndAPIOnOneOrigin(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "index.html"), []byte("production application"), 0o600))
	web, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer web.Close()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/test", r.URL.Path)
		_, _ = io.WriteString(w, "real API handler")
	})
	handler := desktopHandler(web, api, "127.0.0.1:32123", "http://127.0.0.1:32123")
	for _, test := range []struct {
		path, host, origin string
		status             int
		body               string
	}{
		{"/", "127.0.0.1:32123", "", 200, "production application"},
		{"/login", "127.0.0.1:32123", "", 200, "production application"},
		{"/api/test", "127.0.0.1:32123", "http://127.0.0.1:32123", 200, "real API handler"},
		{"/assets/missing.js", "127.0.0.1:32123", "", 404, ""},
		{"/", "rebind.example:32123", "", 403, ""},
		{"/api/test", "127.0.0.1:32123", "https://remote.example", 403, ""},
	} {
		t.Run(test.path+test.host+test.origin, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code)
			if test.body != "" {
				assert.Equal(t, test.body, response.Body.String())
			}
		})
	}
}

func TestDesktopConfigPersistsKeysAndIgnoresServerEnvironment(t *testing.T) {
	options := desktopOptions{DataDir: t.TempDir(), Name: "Configured Desktop"}
	t.Setenv("AUTHN_ISSUER", "https://external.example")
	t.Setenv("DATABASE_MAIN_DSN", "not-the-local-database")
	settings, err := desktopConfig(options, "http://127.0.0.1:32123")
	require.NoError(t, err)
	assert.False(t, settings.Debug)
	assert.True(t, settings.Authn.Enabled)
	assert.False(t, settings.Authn.Web.CookieSecure)
	assert.Empty(t, settings.Authn.Captcha.TestAnswer)
	assert.False(t, settings.Authn.SharedSecurity.DevelopmentServer)
	assert.Equal(t, options.Name, settings.Deployment.Name)
	assert.Equal(t, filepath.Join(options.DataDir, "application.sqlite"), settings.Database["main"].Dsn)
	assert.Equal(t, "http://127.0.0.1:32123/api", settings.Authn.Issuer)
	key, err := os.ReadFile(settings.Authn.SigningKeyFile)
	require.NoError(t, err)
	_, err = desktopConfig(options, "http://127.0.0.1:32123")
	require.NoError(t, err)
	again, err := os.ReadFile(settings.Authn.SigningKeyFile)
	require.NoError(t, err)
	assert.Equal(t, key, again)
}

func TestDesktopFirstSetupRealAPIAndRestartPersistence(t *testing.T) {
	web := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(web, "index.html"), []byte("bundled web"), 0o600))
	options := desktopOptions{DataDir: t.TempDir(), WebDir: web, Name: "Configured Desktop"}
	start := func() (chan desktopEvent, *io.PipeWriter, <-chan error) {
		input, send := io.Pipe()
		output, write := io.Pipe()
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		t.Cleanup(cancel)
		events := make(chan desktopEvent, 4)
		go func() {
			defer close(events)
			decoder := json.NewDecoder(output)
			for {
				var event desktopEvent
				if decoder.Decode(&event) != nil {
					return
				}
				events <- event
			}
		}()
		done := make(chan error, 1)
		go func() {
			done <- runDesktop(ctx, options, input, write)
			_ = write.Close()
			_ = input.Close()
		}()
		t.Cleanup(func() { _ = send.Close(); _ = output.Close() })
		return events, send, done
	}
	next := func(events <-chan desktopEvent, done <-chan error) desktopEvent {
		select {
		case event := <-events:
			return event
		case err := <-done:
			t.Fatalf("desktop ended before readiness: %v", err)
		case <-time.After(60 * time.Second):
			t.Fatal("desktop readiness timed out")
		}
		return desktopEvent{}
	}
	events, input, done := start()
	require.Equal(t, "setup", next(events, done).Event)
	require.NoError(t, json.NewEncoder(input).Encode(desktopRequest{Command: "setup", Login: "owner", Password: "short"}))
	invalid := next(events, done)
	assert.Equal(t, "setup", invalid.Event)
	assert.NotEmpty(t, invalid.Message)
	require.NoError(t, json.NewEncoder(input).Encode(desktopRequest{Command: "setup", Login: "owner", Password: "Desktop-Owner-Password-123!"}))
	ready := next(events, done)
	require.Equal(t, "ready", ready.Event, ready.Message)
	response, err := http.Get(ready.Origin + "/api/deployment")
	require.NoError(t, err)
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, response.Body.Close())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode, string(payload))
	assert.Contains(t, string(payload), options.Name)
	stop := func(input *io.PipeWriter, done <-chan error) {
		require.NoError(t, input.Close())
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Fatal("parent EOF did not stop the local service")
		}
	}
	stop(input, done)
	events, input, done = start()
	restarted := next(events, done)
	require.Equal(t, "ready", restarted.Event, restarted.Message)
	assert.Equal(t, ready.Origin, restarted.Origin)
	// Even an extra request after readiness must not stop the stdin reader
	// observing the parent's subsequent EOF, nor create a second admin.
	require.NoError(t, json.NewEncoder(input).Encode(desktopRequest{Command: "setup", Login: "second", Password: "Another-Password-123!"}))
	assert.Equal(t, "ready", next(events, done).Event)
	stop(input, done)
	response, err = http.Get(ready.Origin + "/")
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	assert.Error(t, err, "owned listener must close on parent EOF")
}

func TestDesktopRefusesIncompleteBundle(t *testing.T) {
	err := runDesktop(t.Context(), desktopOptions{DataDir: t.TempDir(), WebDir: t.TempDir()}, strings.NewReader(""), io.Discard)
	assert.ErrorContains(t, err, "index.html")
}
