package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	webhooki18n "7link.com/platform/internal/iam/webhook/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

var ErrUnavailable = errors.New("webhook service unavailable")

// Module follows each tenant's audit chain and delivers what it finds to the
// endpoints that tenant registered.
type Module struct {
	service   *Service
	runtime   *appenv.Runtime
	registrar *authz.Registrar
	config    Config
	ids       guid.Source

	workerOnce sync.Once
	stop       context.CancelFunc
	stopped    chan struct{}
}

func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("webhook module requires a runtime")
	}
	return &Module{runtime: runtime, config: config, stopped: make(chan struct{})}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name}
}

// InjectServices resolves the two capabilities this module consumes: identifiers
// and the audit event stream. The stream is the only thing it reads, which is
// what keeps every other module free of webhook instrumentation.
func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return err
	}
	reader, err := module.Resolve[audit.EventStreamReader](services)
	if err != nil {
		return err
	}
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return err
	}
	m.ids, m.registrar = ids, registrar

	keys, err := ResolveKeyring(m.config)
	if err != nil {
		return err
	}
	outbound, err := ResolveOutboundPolicy(m.config)
	if err != nil {
		return err
	}
	m.service = NewService(m.runtime, reader, m.config, keys, ids, outbound)
	return m.bindIntakeCallbacks(services)
}

func (m *Module) Initialize(context.Context) error {
	return webhooki18n.Register()
}

// Start follows the chain and sends what is due on a fixed interval. One failed
// pass is logged and retried rather than stopping the worker: a receiver that is
// down must not stop the chain from being followed.
func (m *Module) Start(context.Context) error {
	if m.service == nil {
		return ErrUnavailable
	}
	m.workerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		m.stop = cancel
		go m.run(ctx)
	})
	return nil
}

func (m *Module) run(ctx context.Context) {
	defer close(m.stopped)
	ticker := time.NewTicker(m.config.withDefaults().PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.service.FanOut(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("webhook fan-out pass failed", "error", err)
			}
			if err := m.service.DeliverDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("webhook delivery pass failed", "error", err)
			}
		}
	}
}

// Stop ends the worker and waits for the pass in flight. An attempt the stop
// interrupts still records its outcome, which releases its claim. A process that
// never gets here -- a crash, kill -9, a lost host -- leaves its claim behind, and
// the next delivery pass on any replica takes it back once its lease expires.
func (m *Module) Stop(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	m.stop()
	select {
	case <-m.stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("webhook worker did not stop: %w", ctx.Err())
	}
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.service == nil {
		// Registering the routes anyway lets each one answer unavailable, which
		// is a readable failure; refusing to start the process over a webhook
		// datasource is not.
		slog.Warn("webhook routes registered without a service", "reason", "storage or signing key unavailable",
			"remedy", "check the webhook encryption key and datasource")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

// CheckHealth reports whether the service could be built. The delivery outcome
// counters a deployment alerts on are exported by Collector, not by health: a
// receiver refusing everything must not take this process out of rotation.
func (m *Module) CheckHealth(context.Context) error {
	if m.service == nil {
		return ErrUnavailable
	}
	return nil
}

var (
	_ module.Depender                   = (*Module)(nil)
	_ module.Enabler                    = (*Module)(nil)
	_ module.ServiceInjector            = (*Module)(nil)
	_ module.Initializer                = (*Module)(nil)
	_ module.Starter                    = (*Module)(nil)
	_ module.Stopper                    = (*Module)(nil)
	_ module.HTTPRegistrar              = (*Module)(nil)
	_ module.HealthChecker              = (*Module)(nil)
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
