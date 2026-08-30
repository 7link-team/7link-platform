package module

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedModule struct{ name string }

func (m *namedModule) ModuleName() string { return m.name }

type lifecycleModule struct {
	name        string
	initialize  error
	start       error
	stop        error
	deinitialize error
	events      *[]string
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
	return m.stop
}
func (m *lifecycleModule) Deinitialize(context.Context) error {
	*m.events = append(*m.events, "deinitialize:"+m.name)
	return m.deinitialize
}

type quiesceModule struct {
	name   string
	err    error
	events *[]string
}

func (m *quiesceModule) ModuleName() string { return m.name }
func (m *quiesceModule) Start(context.Context) error {
	return nil
}
func (m *quiesceModule) Quiesce(context.Context) error {
	*m.events = append(*m.events, "quiesce:"+m.name)
	return m.err
}

type enablerModule struct {
	namedModule
	enabled bool
}

func (m *enablerModule) Enabled() bool { return m.enabled }

type dependerModule struct {
	namedModule
	dependencies []string
}

func (m *dependerModule) ModuleDependencies() []string { return m.dependencies }

type dependerEnablerModule struct {
	dependerModule
	enabled bool
}

func (m *dependerEnablerModule) Enabled() bool { return m.enabled }

type deinitializerOnlyModule struct {
	namedModule
	deinitialized bool
}

func (m *deinitializerOnlyModule) Deinitialize(context.Context) error {
	m.deinitialized = true
	return nil
}

type wiringService interface{ Ready() bool }

type wiringProvider struct{}

func (*wiringProvider) ModuleName() string { return "provider" }
func (*wiringProvider) Ready() bool        { return true }
func (*wiringProvider) ProvideServices(services *Services) error {
	return Provide[wiringService](services, &wiringProvider{})
}

type wiringConsumer struct{ service wiringService }

func (*wiringConsumer) ModuleName() string { return "consumer" }
func (c *wiringConsumer) InjectServices(services *Services) error {
	service, err := Resolve[wiringService](services)
	c.service = service
	return err
}

func newRegistered(mods ...Module) *Registry {
	r := NewRegistry()
	_ = r.Register(mods...)
	return r
}

func TestRegisterRejectsNilEmptyAndDuplicateModules(t *testing.T) {
	var nilModule *namedModule
	require.Error(t, NewRegistry().Register(nilModule))
	require.Error(t, NewRegistry().Register(&namedModule{}))
	require.Error(t, NewRegistry().Register(&namedModule{name: "same"}, &namedModule{name: "same"}))

	registry := NewRegistry()
	require.NoError(t, registry.Register(&namedModule{name: "same"}))
	require.Error(t, registry.Register(&namedModule{name: "same"}))
}

func TestRegisterBatchIsAllOrNothing(t *testing.T) {
	r := NewRegistry()
	err := r.Register(&namedModule{name: "ok"}, &namedModule{name: ""})
	require.Error(t, err)
	assert.Empty(t, r.Modules())
}

func TestRegisterPreseedsDeinitializerModules(t *testing.T) {
	events := make([]string, 0)
	dual := &lifecycleModule{name: "dual", events: &events}
	deinitOnly := &deinitializerOnlyModule{namedModule: namedModule{name: "deinit-only"}}
	r := newRegistered(deinitOnly, dual)
	require.Len(t, r.initialized, 2)

	// A failure before Initialize must still release construction-owned resources.
	require.NoError(t, r.Activate())
	require.NoError(t, r.Deinitialize(t.Context()))
	assert.True(t, deinitOnly.deinitialized)
	assert.Contains(t, events, "deinitialize:dual")
}

func TestActivateDropsDisabledModules(t *testing.T) {
	r := newRegistered(
		&namedModule{name: "plain"},
		&enablerModule{namedModule: namedModule{name: "enabled"}, enabled: true},
		&enablerModule{namedModule: namedModule{name: "disabled"}, enabled: false},
	)
	require.NoError(t, r.Activate())
	names := moduleNames(r.Modules())
	assert.Equal(t, []string{"plain", "enabled"}, names)
}

func TestActivateRevalidatesDependenciesOnActiveSet(t *testing.T) {
	t.Run("dependency disabled while dependent active is an error", func(t *testing.T) {
		r := newRegistered(
			&enablerModule{namedModule: namedModule{name: "dep"}, enabled: false},
			&dependerModule{namedModule: namedModule{name: "dependent"}, dependencies: []string{"dep"}},
		)
		err := r.Activate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `module "dependent" requires unregistered module "dep"`)
	})
	t.Run("dependent and dependency both disabled is fine", func(t *testing.T) {
		r := newRegistered(
			&enablerModule{namedModule: namedModule{name: "dep"}, enabled: false},
			&dependerEnablerModule{
				dependerModule: dependerModule{namedModule: namedModule{name: "dependent"}, dependencies: []string{"dep"}},
				enabled:        false,
			},
		)
		require.NoError(t, r.Activate())
		assert.Empty(t, r.Modules())
	})
}

func TestActivateValidatesDependencyOrder(t *testing.T) {
	r := newRegistered(
		&dependerModule{namedModule: namedModule{name: "dependent"}, dependencies: []string{"dep"}},
		&namedModule{name: "dep"},
	)
	err := r.Activate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `module "dependent" must be registered after dependency "dep"`)
}

func TestWireCollectsProvidersBeforeInjection(t *testing.T) {
	consumer := &wiringConsumer{}
	r := newRegistered(consumer, &wiringProvider{})
	require.NoError(t, r.Activate())
	require.NoError(t, r.Wire(nil))
	require.NotNil(t, consumer.service)
	assert.True(t, consumer.service.Ready())
}

func TestLifecycleRollbackOnlyRunsCompletedPhases(t *testing.T) {
	events := make([]string, 0)
	startErr := errors.New("start failed")
	r := newRegistered(
		&lifecycleModule{name: "dependency", events: &events},
		&lifecycleModule{name: "failing", start: startErr, events: &events},
		&lifecycleModule{name: "not-started", events: &events},
	)
	require.NoError(t, r.Activate())
	require.NoError(t, r.Wire(nil))
	require.NoError(t, r.Initialize(t.Context()))
	assert.ErrorIs(t, r.Start(t.Context()), startErr)
	require.NoError(t, r.Stop(t.Context()))
	require.NoError(t, r.Deinitialize(t.Context()))

	assert.Equal(t, []string{
		"initialize:dependency", "initialize:failing", "initialize:not-started",
		"start:dependency", "start:failing",
		"stop:dependency",
		"deinitialize:not-started", "deinitialize:failing", "deinitialize:dependency",
	}, events)
}

func TestInitializeFailureRollsBackConstructionOwnedResources(t *testing.T) {
	events := make([]string, 0)
	initializeErr := errors.New("initialize failed")
	r := newRegistered(
		&lifecycleModule{name: "initialized", events: &events},
		&lifecycleModule{name: "failing", initialize: initializeErr, events: &events},
		&lifecycleModule{name: "not-initialized", events: &events},
	)
	require.NoError(t, r.Activate())
	require.NoError(t, r.Wire(nil))
	assert.ErrorIs(t, r.Initialize(t.Context()), initializeErr)
	require.NoError(t, r.Deinitialize(t.Context()))
	// Every Deinitializer module is pre-seeded at Register (they own resources
	// from construction), so Deinitialize releases all of them in reverse order.
	assert.Equal(t, []string{
		"initialize:initialized", "initialize:failing",
		"deinitialize:not-initialized", "deinitialize:failing", "deinitialize:initialized",
	}, events)
}

func TestQuiesceRunsInReverseOrderAndJoinsErrors(t *testing.T) {
	events := make([]string, 0)
	firstErr := errors.New("first failed")
	secondErr := errors.New("second failed")
	first := &quiesceModule{name: "first", events: &events, err: firstErr}
	second := &quiesceModule{name: "second", events: &events, err: secondErr}
	r := newRegistered(first, &namedModule{name: "plain"}, second)
	require.NoError(t, r.Activate())
	require.NoError(t, r.Wire(nil))
	require.NoError(t, r.Initialize(t.Context()))
	require.NoError(t, r.Start(t.Context()))

	err := r.Quiesce(t.Context())
	assert.ErrorIs(t, err, firstErr)
	assert.ErrorIs(t, err, secondErr)
	assert.Equal(t, []string{"quiesce:second", "quiesce:first"}, events)
}

func TestStopAndDeinitializeAreIdempotentAndClearLists(t *testing.T) {
	events := make([]string, 0)
	r := newRegistered(&lifecycleModule{name: "m", events: &events})
	require.NoError(t, r.Activate())
	require.NoError(t, r.Wire(nil))
	require.NoError(t, r.Initialize(t.Context()))
	require.NoError(t, r.Start(t.Context()))
	require.NoError(t, r.Stop(t.Context()))
	require.NoError(t, r.Stop(t.Context())) // second shutdown is a no-op
	require.NoError(t, r.Deinitialize(t.Context()))
	require.NoError(t, r.Deinitialize(t.Context()))
	assert.Equal(t, []string{"initialize:m", "start:m", "stop:m", "deinitialize:m"}, events)
}

func TestRegisterHTTPRequiresStarted(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(&namedModule{name: "m"}))
	require.NoError(t, r.Activate())
	err := r.RegisterHTTP(t.Context(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "register http requires started modules")
}

func TestModulesReturnsDefensiveCopy(t *testing.T) {
	r := newRegistered(&namedModule{name: "a"}, &namedModule{name: "b"})
	mods := r.Modules()
	mods[0] = &namedModule{name: "mutated"}
	assert.Equal(t, "a", r.Modules()[0].ModuleName())
	_, ok := r.ByName("a")
	assert.True(t, ok)
	_, ok = r.ByName("missing")
	assert.False(t, ok)
}

func moduleNames(mods []Module) []string {
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		names = append(names, m.ModuleName())
	}
	return names
}
