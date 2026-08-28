package app

import (
	"net"
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

func validTestAppConfig() AppConfig {
	return AppConfig{
		Name:       "test-api",
		Version:    "1.0.0",
		Timezone:   "UTC",
		DrainDelay: time.Nanosecond,
		API: API{
			Landing: APILanding{Enabled: true},
			Docs:    APIDocs{Enabled: true, Advertise: true},
			OpenAPI: APIOpenAPI{Enabled: true, Advertise: true},
		},
		HttpServer: HttpServer{Address: "127.0.0.1", Port: 8080},
		GrpcServer: GrpcServer{Address: "127.0.0.1", Port: 9090},
	}
}
