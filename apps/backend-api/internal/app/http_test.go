package app

import (
	"errors"
	"net"
	"testing"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartHTTPServerReportsRegistrationAndServeErrors(t *testing.T) {
	registrationErr := errors.New("registration failed")
	app := &App{
		config: validTestAppConfig(),
		ctx:    t.Context(),
		mods:   []module.Module{&registrarModule{httpErr: registrationErr}},
	}
	assert.ErrorIs(t, app.startHTTPServer(), registrationErr)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	app.mods = nil
	app.config.HttpServer.Port = listener.Addr().(*net.TCPAddr).Port
	app.serveErr = make(chan error, 1)
	require.NoError(t, app.startHTTPServer())
	select {
	case err := <-app.serveErr:
		assert.ErrorContains(t, err, "http serve")
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP server did not report its bind error")
	}
}

func TestStartHTTPServerRequiresConfiguredMetricsModule(t *testing.T) {
	app := &App{config: validTestAppConfig()}
	app.config.API.Metrics.Enabled = true
	assert.EqualError(t, app.startHTTPServer(), "http: prometheus module is unavailable")
}
