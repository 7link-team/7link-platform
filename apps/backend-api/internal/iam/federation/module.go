package federation

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	federationi18n "7link.com/platform/internal/iam/federation/i18n"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

var ErrUnavailable = errors.New("federation service unavailable")

type Module struct {
	service         *Service
	runtime         *appenv.Runtime
	registrar       *authz.Registrar
	config          Config
	ids             guid.Source
	appender        audit.Appender
	identities      identity.IdentityProvisioner
	authn           authn.WebService
	declarationOnly bool
}

func NewModule(runtime *appenv.Runtime, registrar *authz.Registrar, config Config) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() {
		panic("federation module requires runtime and authz registrar")
	}
	return &Module{runtime: runtime, registrar: registrar, config: config}
}

func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("federation declaration module requires a declaration-only authz registrar")
	}
	return &Module{registrar: registrar, config: Config{Config: module.Config{Enabled: true}}, declarationOnly: true}
}

func (m *Module) Enabled() bool {
	return m != nil && (m.config.Enabled || m.config.SAML.Enabled)
}

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, identity.Name, authn.Name}
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve federation id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve federation audit appender: %w", err)
	}
	identities, err := module.Resolve[identity.IdentityProvisioner](services)
	if err != nil {
		return fmt.Errorf("resolve federation identity provisioner: %w", err)
	}
	authenticator, err := module.Resolve[authn.WebService](services)
	if err != nil {
		return fmt.Errorf("resolve federation authentication service: %w", err)
	}
	m.ids, m.appender, m.identities, m.authn = ids, appender, identities, authenticator
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if m == nil {
		return errors.New("initialize federation: module is required")
	}
	if err := federationi18n.Register(); err != nil {
		return fmt.Errorf("register federation i18n: %w", err)
	}
	if m.declarationOnly {
		return nil
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil ||
		m.registrar == nil || m.ids == nil || m.appender == nil || m.identities == nil || m.authn == nil {
		return errors.New("initialize federation: runtime database, authz registrar, id generator, audit appender, identity provisioner, and authentication service are required")
	}
	key, err := ResolveEncryptionKey(m.config)
	if err != nil {
		return fmt.Errorf("resolve federation encryption key: %w", err)
	}
	m.service = NewService(m.runtime, m.appender, m.identities, m.authn, m.config, key, m.ids)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.declarationOnly {
		return nil
	}
	if m.service == nil {
		return ErrUnavailable
	}
	return m.service.StartSAML(ctx, m.config.SAML)
}

func (m *Module) CheckHealth(context.Context) error {
	if m != nil && m.declarationOnly {
		return nil
	}
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register federation: module is unavailable")
	}
	if !m.declarationOnly && m.service == nil {
		return errors.New("register federation: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
