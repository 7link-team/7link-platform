package authn

import (
	"context"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	authni18n "7link.com/platform/internal/iam/authn/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

// Module owns authentication credentials, sessions, factors, challenges,
// browser authentication HTTP operations, and their worker lifecycle.
type Module struct {
	*Service
	runtime  *appenv.Runtime
	config   authnext.Config
	options  []WebOption
	ids      guid.Source
	appender audit.Appender
}

// NewModule builds the authn module from its runtime, authentication config,
// and explicitly supplied web-service collaborators. Required shared services
// such as ID generation and audit append are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config authnext.Config, options ...WebOption) *Module {
	return &Module{runtime: runtime, config: config, options: options}
}

// Enabled is evaluated before Initialize, so it must use construction-time
// configuration rather than the embedded service created during Initialize.
func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[WebService](services, m); err != nil {
		return fmt.Errorf("provide authn web service: %w", err)
	}
	if err := module.Provide[InvitationCredentials](services, m); err != nil {
		return fmt.Errorf("provide authn invitation credentials: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve authn id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve authn audit appender: %w", err)
	}
	m.ids = ids
	m.appender = appender
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := authni18n.Register(); err != nil {
		return fmt.Errorf("register authn i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
		return fmt.Errorf("initialize authn: runtime, id generator, and audit appender are required")
	}
	repository := NewRepository(m.runtime, m.ids, m.appender)
	service, err := NewWebService(m.config, repository, m.options...)
	if err != nil {
		return fmt.Errorf("initialize authn service: %w", err)
	}
	m.Service = service
	return nil
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.Service == nil || m.repo == nil || m.repo.writer() == nil {
		return authnext.ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("register authn: service is unavailable")
	}
	RegisterHTTP(api, m.Service, m.Service)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("start authn: service is unavailable")
	}
	return m.StartNotificationWorker(ctx)
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return nil
	}
	return m.StopNotificationWorker(ctx)
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ WebService             = (*Module)(nil)
	_ InvitationCredentials  = (*Module)(nil)
)
