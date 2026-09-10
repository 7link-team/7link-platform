package oauth

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

// ErrUnavailable reports that the OAuth module has not been initialized with
// a writable repository.
var ErrUnavailable = errors.New("OAuth service unavailable")

// Module owns OAuth protocol persistence, client administration, HTTP, and
// locale behavior.
type Config struct {
	module.Config
	// Consent locates the consent screen and bounds how long a parked
	// authorization request stays answerable.
	Consent ConsentConfig
}

type Module struct {
	*Service
	config    Config
	runtime   *appenv.Runtime
	registrar *authz.Registrar
	authn     authn.WebService
	ids       guid.Source
	appender  audit.Appender
}

// NewModule builds the OAuth module. Shared business and infrastructure
// services are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("OAuth module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, authn.Name}
}

// ProvideServices exposes grant validation without making authn depend on OAuth.
func (m *Module) ProvideServices(services *module.Services) error {
	return module.Provide[authn.OAuthGrantValidator](services, authn.OAuthGrantValidator(func(ctx context.Context, claims *authnext.Claims) error {
		if m.Service == nil {
			return authnext.ErrUnavailable
		}
		return m.Service.ValidateAccessGrant(ctx, claims)
	}))
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth authorization registrar: %w", err)
	}
	authenticator, err := module.Resolve[authn.WebService](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth authentication service: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth audit appender: %w", err)
	}
	m.registrar = registrar
	m.authn = authenticator
	m.ids = ids
	m.appender = appender
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := oauthi18n.Register(); err != nil {
		return fmt.Errorf("register OAuth i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.registrar == nil || m.authn == nil || m.ids == nil || m.appender == nil {
		return errors.New("initialize OAuth: runtime, authz registrar, authentication, id generator, and audit appender are required")
	}
	m.Service = NewService(m.runtime, m.authn, m.ids, m.appender, m.config.Consent)
	// Parse the consent screen now: a misconfigured template file should stop
	// the process here rather than surface as a 500 on someone's first login.
	if _, err := m.Service.consentTemplate(); err != nil {
		return fmt.Errorf("initialize OAuth consent screen: %w", err)
	}
	return nil
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.Service == nil || m.Service.repo == nil || m.Service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.Service == nil || m.registrar == nil {
		return errors.New("register OAuth: service is unavailable")
	}
	RegisterHTTP(api, m.Service, m.registrar)
	return nil
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
