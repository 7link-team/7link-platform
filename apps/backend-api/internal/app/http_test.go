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

func TestStartHTTPServerReportsRegistrationAndListenErrors(t *testing.T) {
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
	assert.ErrorContains(t, app.startHTTPServer(), "http listen")
}

func TestStartHTTPServerRequiresConfiguredMetricsModule(t *testing.T) {
	app := &App{config: validTestAppConfig()}
	app.config.API.Metrics.Enabled = true
	assert.EqualError(t, app.startHTTPServer(), "http: prometheus module is unavailable")
}

func TestStartHTTPServerAppliesHardeningConfiguration(t *testing.T) {
	app := &App{
		config:   validTestAppConfig(),
		ctx:      t.Context(),
		serveErr: make(chan error, 1),
	}
	app.config.HttpServer.Port = 0
	app.config.HttpServer.ReadHeaderTimeout = 3 * time.Second
	app.config.HttpServer.IdleTimeout = 45 * time.Second
	app.config.HttpServer.MaxHeaderBytes = 8192
	require.NoError(t, app.startHTTPServer())
	require.NotNil(t, app.httpServer)
	assert.Equal(t, 3*time.Second, app.httpServer.ReadHeaderTimeout)
	assert.Equal(t, 45*time.Second, app.httpServer.IdleTimeout)
	assert.Equal(t, 8192, app.httpServer.MaxHeaderBytes)
	require.NoError(t, app.httpServer.Shutdown(t.Context()))
}
