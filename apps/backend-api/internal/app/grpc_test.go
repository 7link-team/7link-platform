package app

import (
	"net"
	"testing"
	"time"

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

func TestGRPCServerOptionsFollowConfiguration(t *testing.T) {
	assert.Empty(t, grpcServerOptions(GrpcServer{}))
	options := grpcServerOptions(GrpcServer{
		MaxRecvMessageBytes: 1024,
		MaxSendMessageBytes: 2048,
		MaxConnectionIdle:   time.Minute,
		KeepaliveMinTime:    30 * time.Second,
		PermitWithoutStream: true,
	})
	assert.Len(t, options, 4)
}
