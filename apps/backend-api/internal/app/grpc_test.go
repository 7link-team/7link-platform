package app

import (
	"context"
	"net"
	"testing"
	"time"

	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditDependencies returns the modules audit depends on transitively
// (i18n <- guid <- wuid), so Activate's dependency validation passes.
func auditDependencies(rt *appenv.Runtime) []module.Module {
	return []module.Module{
		i18n.NewModule(rt, i18n.Base),
		wuid.NewModule(rt),
		guid.NewModule(rt),
	}
}

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
	rt := appenv.NewRuntime(appenv.NewIdentity(app.config.Name, app.config.Version))
	auditModule := audit.NewModule(rt, audit.Config{Enabled: true})
	// The interceptor is collected from the module registry, so the module (and
	// its transitive dependencies) must be registered and started for the
	// mounting path to execute.
	deps := auditDependencies(rt)
	app.modules = wiredRegistry(t, append(deps, auditModule)...)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, app.modules.Stop(stopCtx))
	})
	require.NoError(t, app.startGRPCServer())
	require.NotNil(t, app.grpcServer)
	app.grpcServer.Stop()
}

func TestGRPCProtocolOptionsCollectsModuleInterceptors(t *testing.T) {
	app := &App{ctx: t.Context()}
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	auditModule := audit.NewModule(rt, audit.Config{Enabled: true})
	deps := auditDependencies(rt)
	app.modules = registeredRegistry(append(deps, auditModule)...)
	options := app.modules.GRPCProtocolOptions()
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
