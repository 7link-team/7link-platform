package app

import (
	"testing"

	configx "7link.com/platform/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseRolesDefaultToReadWriteDuringParsing(t *testing.T) {
	var parsed AppConfig
	require.NoError(t, configx.New().Parse(&parsed, "--database.main.type=sqlite", "--database.main.dsn=:memory:"))
	database := parsed.Database["main"]
	assert.True(t, database.Writable)
	assert.True(t, database.Readable)
}

func TestPprofDefaultsToDisabledAndCanBeEnabled(t *testing.T) {
	var disabled AppConfig
	require.NoError(t, configx.New().Parse(&disabled))
	assert.False(t, disabled.Pprof.Enabled)

	var enabled AppConfig
	require.NoError(t, configx.New().Parse(&enabled, "--pprof.enabled"))
	assert.True(t, enabled.Pprof.Enabled)
}

func TestHTTPAPIResourceDefaultsAndOverrides(t *testing.T) {
	var defaults AppConfig
	require.NoError(t, configx.New().Parse(&defaults))
	assert.True(t, defaults.API.Landing.Enabled)
	assert.False(t, defaults.API.Landing.AdvertiseHealth)
	assert.Equal(t, APIDocs{Enabled: true, Advertise: true}, defaults.API.Docs)
	assert.Equal(t, APIOpenAPI{Enabled: true, Advertise: true}, defaults.API.OpenAPI)

	var configured AppConfig
	require.NoError(t, configx.New().Parse(&configured,
		"--api.landing.enabled=false",
		"--api.landing.advertise_health=true",
		"--api.docs.advertise=false",
		"--api.docs.enabled=false",
	))
	assert.False(t, configured.API.Landing.Enabled)
	assert.True(t, configured.API.Landing.AdvertiseHealth)
	assert.Equal(t, APIDocs{Enabled: false, Advertise: false}, configured.API.Docs)
}

func TestServeMigrationDefaultsToDisabledAndCanBeEnabled(t *testing.T) {
	var disabled AppConfig
	require.NoError(t, configx.New().Parse(&disabled))
	assert.False(t, disabled.Migrate)

	var enabled AppConfig
	require.NoError(t, configx.New().Parse(&enabled, "--migrate"))
	assert.True(t, enabled.Migrate)
}

func TestServerAddressesRequireIPv4(t *testing.T) {
	valid := AppConfig{
		HttpServer: HttpServer{Address: "0.0.0.0"},
		GrpcServer: GrpcServer{Address: "0.0.0.0"},
	}
	require.NoError(t, configx.Process(&valid))

	invalidHTTP := valid
	invalidHTTP.HttpServer.Address = "localhost"
	assert.EqualError(t, configx.Process(&invalidHTTP), "config: http_server.address does not match validation pattern")

	invalidGRPC := valid
	invalidGRPC.GrpcServer.Address = "::"
	assert.EqualError(t, configx.Process(&invalidGRPC), "config: grpc_server.address does not match validation pattern")
}
