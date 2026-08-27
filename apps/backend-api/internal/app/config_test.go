package app

import (
	"testing"

	configx "7link.com/platform/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
