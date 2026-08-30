package app

import (
	"context"
	"net"
	"testing"
	"time"

	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/pkg/module"
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

func TestStartGRPCServerMountsAuditInterceptor(t *testing.T) {
	app := &App{
		config:   validTestAppConfig(),
		ctx:      t.Context(),
		serveErr: make(chan error, 2),
	}
	app.config.GrpcServer.Port = 0
	app.audit = audit.NewModule(app.config.Name, app.config.Version, nil, audit.Config{Enabled: true})
	// The interceptor is collected from the module registry, so the module must
	// actually be registered for the mounting path to execute.
	app.mods = []module.Module{app.audit}
	require.NoError(t, app.audit.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, app.audit.Stop(stopCtx))
	})
	require.NoError(t, app.startGRPCServer())
	require.NotNil(t, app.grpcServer)
	app.grpcServer.Stop()
}

func TestGRPCProtocolOptionsCollectsModuleInterceptors(t *testing.T) {
	app := &App{ctx: t.Context()}
	app.audit = audit.NewModule("test", "1", nil, audit.Config{Enabled: true})
	app.mods = []module.Module{app.audit}
	options := app.grpcProtocolOptions()
	// The audit module contributes no stats handler but one unary and one stream
	// chain; the platform interceptors (locale, recovery, validation) follow.
	assert.Len(t, options, 2)
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
