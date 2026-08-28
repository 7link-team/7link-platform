package app

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartGRPCServerRejectsOccupiedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	app := &App{config: validTestAppConfig()}
	app.config.GrpcServer.Port = listener.Addr().(*net.TCPAddr).Port
	err = app.startGRPCServer()
	assert.ErrorContains(t, err, "grpc listen")
}

func TestStartGRPCServerWithoutTelemetry(t *testing.T) {
	app := &App{
		config:   validTestAppConfig(),
		ctx:      t.Context(),
		serveErr: make(chan error, 2),
	}
	app.config.GrpcServer.Port = 0
	require.NoError(t, app.startGRPCServer())
	require.NotNil(t, app.grpcServer)
	app.grpcServer.Stop()
}
