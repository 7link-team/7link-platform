package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBusinessSecretsConfig provisions a fresh, test-lifetime business key.
// It deliberately does not reuse authentication keys or production defaults.
func testBusinessSecretsConfig(t *testing.T) BusinessSecretsConfig {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "business.key")
	require.NoError(t, os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0600))
	return BusinessSecretsConfig{Enabled: true, ActiveKeyFile: path}
}

func TestBusinessSecretsFixtureKeysAreIsolatedAndRequired(t *testing.T) {
	first := secrets.NewModule(businessSecretsConfig(AppConfig{Secrets: testBusinessSecretsConfig(t)}))
	second := secrets.NewModule(businessSecretsConfig(AppConfig{Secrets: testBusinessSecretsConfig(t)}))
	for _, sealer := range []*secrets.Module{first, second} {
		require.NoError(t, sealer.Initialize(t.Context()))
		require.NoError(t, sealer.RegisterPurpose("fixture.secret"))
	}
	aad, err := secrets.AssociatedData(1, 2)
	require.NoError(t, err)
	envelope, err := first.Seal("fixture.secret", aad, []byte("fixture value"))
	require.NoError(t, err)
	plain, err := first.Open("fixture.secret", aad, envelope)
	require.NoError(t, err)
	require.Equal(t, "fixture value", string(plain))
	_, err = second.Open("fixture.secret", aad, envelope)
	require.Error(t, err, "another fixture must not share its business key")
	missing := secrets.NewModule(businessSecretsConfig(AppConfig{Secrets: BusinessSecretsConfig{Enabled: true}}))
	require.Error(t, missing.Initialize(t.Context()))
	_, err = missing.Seal("fixture.secret", aad, []byte("fixture value"))
	require.ErrorIs(t, err, secrets.ErrUnavailable)
}

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
	assert.ErrorIs(t, app.awaitShutdown(make(chan os.Signal)), want)
}

// TestAwaitShutdownHandlesSignal verifies the shutdown logic responds to a
// signal value. The signal channel is injected rather than delivered by the OS,
// because Windows does not support process-level SIGTERM delivery; production
// wires the channel to os/signal.Notify in Run.
func TestAwaitShutdownHandlesSignal(t *testing.T) {
	for _, graceful := range []bool{true, false} {
		t.Run(map[bool]string{true: "graceful", false: "immediate"}[graceful], func(t *testing.T) {
			app := &App{
				config:   AppConfig{Shutdown: ShutdownConfig{Graceful: graceful, Timeout: time.Second}},
				serveErr: make(chan error),
			}
			signals := make(chan os.Signal, 1)
			go func() {
				time.Sleep(20 * time.Millisecond)
				signals <- syscall.SIGTERM
			}()
			require.NoError(t, app.awaitShutdown(signals))
		})
	}
}

func TestAddModulePropagatesRegistryValidation(t *testing.T) {
	app := &App{modules: module.NewRegistry()}
	first := &namedHealthModule{name: "same"}
	require.NoError(t, app.addModule(first))
	assert.ErrorContains(t, app.addModule(&namedHealthModule{name: "same"}), "duplicate module")
	assert.EqualError(t, (&App{}).addModule(first), "module registry is not composed")
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
