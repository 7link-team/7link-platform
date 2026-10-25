// Package realtime owns database-backed event fanout and authenticated SSE.
package realtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

const Name = "realtime"

var (
	ErrUnavailable = errors.New("realtime unavailable")
	ErrInvalid     = errors.New("realtime invalid request")
	ErrLimit       = errors.New("realtime connection limit")
)

type Config struct{ module.Config }
type Topic struct {
	TenantID, ID guid.ID
	Kind         string
	Collection   bool
}
type TopicAuthorizer func(context.Context, guid.ID, guid.ID, Topic) (bool, error)
type TopicAuthorizers struct {
	Register func(string, TopicAuthorizer) error
}
type Broadcaster func(context.Context, string, any) error

type Module struct {
	runtime                     *appenv.Runtime
	config                      Config
	registrar                   *authz.Registrar
	tail                        events.Tail
	wake                        events.Wakeups
	publisher                   events.EphemeralPublisher
	hub                         *Hub
	mu                          sync.RWMutex
	authorizers                 map[string]TopicAuthorizer
	sealed                      bool
	pending                     map[string]broadcast
	heartbeat, reauth, lifetime time.Duration
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, hub: newHub(), authorizers: map[string]TopicAuthorizer{}, pending: map[string]broadcast{}, heartbeat: 15 * time.Second, reauth: 60 * time.Second, lifetime: 30 * time.Minute}
}
func (*Module) ModuleName() string           { return Name }
func (m *Module) Enabled() bool              { return m.config.Enabled }
func (*Module) ModuleDependencies() []string { return []string{events.Name, core.Name} }
func (m *Module) ProvideServices(s *module.Services) error {
	if err := module.Provide[*Hub](s, m.hub); err != nil {
		return err
	}
	if err := module.Provide[Broadcaster](s, m.Broadcast); err != nil {
		return err
	}
	return module.Provide[TopicAuthorizers](s, TopicAuthorizers{Register: m.register})
}
func (m *Module) InjectServices(s *module.Services) error {
	var err error
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.tail, err = module.Resolve[events.Tail](s); err != nil {
		return err
	}
	if m.wake, err = module.Resolve[events.Wakeups](s); err != nil {
		return err
	}
	if m.publisher, err = module.Resolve[events.EphemeralPublisher](s); err != nil {
		return err
	}
	return nil
}
func (m *Module) register(kind string, fn TopicAuthorizer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed || !kindRE.MatchString(kind) || kind == "inbox" || fn == nil || m.authorizers[kind] != nil {
		return ErrInvalid
	}
	m.authorizers[kind] = fn
	return nil
}
func (m *Module) Initialize(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registrar == nil || m.tail == nil || m.publisher == nil {
		return ErrUnavailable
	}
	m.sealed = true
	return nil
}
func (m *Module) Start(ctx context.Context) error {
	m.mu.RLock()
	ready := m.sealed
	m.mu.RUnlock()
	if !ready {
		return ErrUnavailable
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		slog.WarnContext(ctx, "realtime stream unavailable without a writable datasource")
		return nil
	}
	return m.hub.start(ctx, m.tail, m.flushBroadcasts, m.wake)
}
func (m *Module) Quiesce(ctx context.Context) error { return m.hub.quiesce(ctx) }
func (m *Module) Stop(ctx context.Context) error    { return m.Quiesce(ctx) }
func (m *Module) Collector() prometheus.Collector   { return m.hub }

var (
	_ module.Module                    = (*Module)(nil)
	_ module.Enabler                   = (*Module)(nil)
	_ module.Depender                  = (*Module)(nil)
	_ module.ServiceProvider           = (*Module)(nil)
	_ module.ServiceInjector           = (*Module)(nil)
	_ module.Initializer               = (*Module)(nil)
	_ module.Starter                   = (*Module)(nil)
	_ module.Quiescer                  = (*Module)(nil)
	_ module.Stopper                   = (*Module)(nil)
	_ module.HTTPRegistrar             = (*Module)(nil)
	_ module.PrometheusCollectorModule = (*Module)(nil)
)
