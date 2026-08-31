package app

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRejectsInvalidConfigurationAndTimezone(t *testing.T) {
	invalidConfig := validTestAppConfig()
	invalidConfig.HttpServer.Address = "localhost"
	assert.ErrorContains(t, (&App{}).Run(invalidConfig), "http_server.address does not match validation pattern")

	invalidTimezone := validTestAppConfig()
	invalidTimezone.Timezone = "Invalid/Timezone"
	assert.ErrorContains(t, (&App{}).Run(invalidTimezone), `initialize timezone "Invalid/Timezone"`)

}

func TestRunShutsDownWhenHTTPServerCannotBind(t *testing.T) {
	t.Setenv("WUID", "7")
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer httpListener.Close()

	config := validTestAppConfig()
	config.HttpServer.Port = httpListener.Addr().(*net.TCPAddr).Port
	config.GrpcServer.Port = 0

	err = (&App{}).Run(config)
	assert.ErrorContains(t, err, "http listen")
}

func TestRunShutsDownWhenGRPCServerCannotBind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	config := validTestAppConfig()
	config.GrpcServer.Port = listener.Addr().(*net.TCPAddr).Port
	err = (&App{}).Run(config)
	assert.ErrorContains(t, err, "grpc listen")
}

func TestAwaitShutdownPropagatesServeFailureAndCleansUp(t *testing.T) {
	want := errors.New("serve failed")
	app := &App{
		config:   AppConfig{Shutdown: ShutdownConfig{Graceful: false, Timeout: time.Second}},
		serveErr: make(chan error, 1),
	}
	app.serveErr <- want
	assert.ErrorIs(t, app.awaitShutdown(), want)
}

func TestAwaitShutdownHandlesProcessSignal(t *testing.T) {
	for _, graceful := range []bool{true, false} {
		t.Run(map[bool]string{true: "graceful", false: "immediate"}[graceful], func(t *testing.T) {
			app := &App{
				config:   AppConfig{Shutdown: ShutdownConfig{Graceful: graceful, Timeout: time.Second}},
				serveErr: make(chan error),
			}
			go func() {
				time.Sleep(20 * time.Millisecond)
				process, err := os.FindProcess(os.Getpid())
				if err == nil {
					_ = process.Signal(syscall.SIGTERM)
				}
			}()
			require.NoError(t, app.awaitShutdown())
		})
	}
}

func TestAddModulePropagatesRegistryValidation(t *testing.T) {
	app := &App{}
	first := &namedHealthModule{name: "same"}
	require.NoError(t, app.addModule(first))
	assert.ErrorContains(t, app.addModule(&namedHealthModule{name: "same"}), "duplicate module")
}

func validTestAppConfig() AppConfig {
	return AppConfig{
		Name:     "test-api",
		Version:  "1.0.0",
		Timezone: "UTC",
		Shutdown: ShutdownConfig{Graceful: true, DrainDelay: time.Nanosecond},
		API: API{
			Landing: APILanding{Enabled: true},
			Docs:    APIDocs{Enabled: true, Advertise: true},
			OpenAPI: APIOpenAPI{Enabled: true, Advertise: true},
		},
		HttpServer: HttpServer{Address: "127.0.0.1", Port: 8080},
		GrpcServer: GrpcServer{Address: "127.0.0.1", Port: 9090},
		// Audit defaults to enabled in production; reflect that in tests.
		Audit: Audit{Enabled: true},
	}
}
