package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type registrarModule struct {
	httpErr       error
	httpCalled    bool
	grpcCalled    bool
	registeredCtx context.Context
}

func (*registrarModule) ModuleName() string { return "registrar" }

func (m *registrarModule) RegisterHTTP(ctx context.Context, _ chi.Router, _ huma.API) error {
	m.httpCalled = true
	m.registeredCtx = ctx
	return m.httpErr
}

func (m *registrarModule) RegisterGRPC(grpc.ServiceRegistrar) { m.grpcCalled = true }

func TestModuleRegistration(t *testing.T) {
	registrationErr := errors.New("registration failed")
	registrar := &registrarModule{httpErr: registrationErr}
	app := &App{mods: []module.Module{&namedModule{name: "plain"}, registrar}}

	err := app.registerHTTPModules(t.Context(), chi.NewRouter(), nil)
	assert.ErrorIs(t, err, registrationErr)
	assert.True(t, registrar.httpCalled)
	assert.Same(t, t.Context(), registrar.registeredCtx)

	app.registerGRPCModules(grpc.NewServer())
	assert.True(t, registrar.grpcCalled)
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

func TestStartHTTPServerReportsRegistrationAndServeErrors(t *testing.T) {
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
	require.NoError(t, app.startHTTPServer())
	select {
	case err := <-app.serveErr:
		assert.ErrorContains(t, err, "http serve")
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP server did not report its bind error")
	}
}
