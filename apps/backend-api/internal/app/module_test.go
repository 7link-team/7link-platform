package app

import (
	"context"
	"errors"
	"testing"

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

type lifecycleModule struct {
	name       string
	initialize error
	start      error
	events     *[]string
}

func (m *lifecycleModule) ModuleName() string { return m.name }
func (m *lifecycleModule) Initialize(context.Context) error {
	*m.events = append(*m.events, "initialize:"+m.name)
	return m.initialize
}
func (m *lifecycleModule) Start(context.Context) error {
	*m.events = append(*m.events, "start:"+m.name)
	return m.start
}
func (m *lifecycleModule) Stop(context.Context) error {
	*m.events = append(*m.events, "stop:"+m.name)
	return nil
}
func (m *lifecycleModule) Deinitialize(context.Context) error {
	*m.events = append(*m.events, "deinitialize:"+m.name)
	return nil
}

func TestLifecycleRollbackOnlyRunsCompletedPhases(t *testing.T) {
	events := make([]string, 0)
	startErr := errors.New("start failed")
	app := &App{mods: []module.Module{
		&lifecycleModule{name: "dependency", events: &events},
		&lifecycleModule{name: "failing", start: startErr, events: &events},
		&lifecycleModule{name: "not-started", events: &events},
	}}
	require.NoError(t, app.wireModules())
	require.NoError(t, app.initializeModules(t.Context()))
	assert.ErrorIs(t, app.startModules(t.Context()), startErr)
	require.NoError(t, app.stopModules(t.Context()))
	require.NoError(t, app.deinitializeModules(t.Context()))

	assert.Equal(t, []string{
		"initialize:dependency", "initialize:failing", "initialize:not-started",
		"start:dependency", "start:failing",
		"stop:dependency",
		"deinitialize:not-started", "deinitialize:failing", "deinitialize:dependency",
	}, events)
}

func TestInitializeFailureRollsBackOnlySuccessfulModules(t *testing.T) {
	events := make([]string, 0)
	initializeErr := errors.New("initialize failed")
	app := &App{mods: []module.Module{
		&lifecycleModule{name: "initialized", events: &events},
		&lifecycleModule{name: "failing", initialize: initializeErr, events: &events},
		&lifecycleModule{name: "not-initialized", events: &events},
	}}
	require.NoError(t, app.wireModules())
	assert.ErrorIs(t, app.initializeModules(t.Context()), initializeErr)
	require.NoError(t, app.deinitializeModules(t.Context()))
	assert.Equal(t, []string{
		"initialize:initialized", "initialize:failing", "deinitialize:initialized",
	}, events)
}

type namedModule struct{ name string }

func (m *namedModule) ModuleName() string { return m.name }

type dependentModule struct{ namedModule }

func (*dependentModule) ModuleDependencies() []string { return []string{"dependency"} }

func TestWireModulesRejectsNilEmptyAndDuplicateModules(t *testing.T) {
	var nilModule *namedModule
	for _, app := range []*App{
		{mods: []module.Module{nilModule}},
		{mods: []module.Module{&namedModule{}}},
		{mods: []module.Module{&namedModule{name: "same"}, &namedModule{name: "same"}}},
	} {
		assert.Error(t, app.wireModules())
	}
}

func TestWireModulesValidatesLifecycleDependencyOrder(t *testing.T) {
	valid := &App{mods: []module.Module{
		&namedModule{name: "dependency"},
		&dependentModule{namedModule{name: "dependent"}},
	}}
	require.NoError(t, valid.wireModules())

	wrongOrder := &App{mods: []module.Module{
		&dependentModule{namedModule{name: "dependent"}},
		&namedModule{name: "dependency"},
	}}
	assert.ErrorContains(t, wrongOrder.wireModules(), `must be registered after dependency "dependency"`)
}
