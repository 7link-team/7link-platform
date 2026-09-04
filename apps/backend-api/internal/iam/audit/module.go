package audit

import (
	"context"
	"errors"
	"fmt"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("audit service unavailable")

// Module owns the IAM append-only audit chain, retention policy, anchoring,
// HTTP operations, and the Appender capability consumed by other IAM modules.
type Module struct {
	service         *Service
	runtime         *appenv.Runtime
	registrar       *authz.Registrar
	config          Config
	ids             guid.Source
	declarationOnly bool
}

func NewModule(runtime *appenv.Runtime, registrar *authz.Registrar, config Config) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() {
		panic("audit module requires runtime and authz registrar")
	}
	return &Module{runtime: runtime, registrar: registrar, config: config}
}

func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("audit declaration module requires a declaration-only authz registrar")
	}
	return &Module{registrar: registrar, config: Config{Config: module.Config{Enabled: true}}, declarationOnly: true}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{platformi18n.Name, guid.Name} }

func (m *Module) ProvideServices(services *module.Services) error {
	if m != nil && m.declarationOnly {
		return nil
	}
	if err := module.Provide[Appender](services, Appender(m.appendDomain)); err != nil {
		return fmt.Errorf("provide audit appender: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve audit id generator: %w", err)
	}
	m.ids = ids
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := auditi18n.Register(); err != nil {
		return fmt.Errorf("register audit i18n: %w", err)
	}
	if m == nil {
		return errors.New("initialize audit: module is required")
	}
	if m.declarationOnly {
		return nil
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.registrar == nil || m.ids == nil {
		return errors.New("initialize audit: runtime database, authz registrar, and id generator are required")
	}
	m.service = NewService(m.runtime, m.ids)
	return nil
}

func (m *Module) Start(context.Context) error {
	if m == nil || m.declarationOnly || !m.config.Anchor.Enabled {
		return nil
	}
	if m.service == nil {
		return ErrUnavailable
	}
	store, err := NewAnchorStore(m.config.Anchor)
	if err != nil {
		return fmt.Errorf("audit anchor configuration: %w", err)
	}
	service := NewServiceWithAnchor(m.runtime, store, m.ids)
	if m.config.Anchor.SigningKey != "" {
		signer, err := NewRootSigner(m.config.Anchor.SigningKey)
		if err != nil {
			return fmt.Errorf("audit anchor signing key: %w", err)
		}
		service = NewServiceWithAnchorAndSigner(m.runtime, store, signer, m.ids)
	}
	m.service = service
	return nil
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
		return errors.New("register audit: module is unavailable")
	}
	if !m.declarationOnly && m.service == nil {
		return errors.New("register audit: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

func (m *Module) appendDomain(ctx context.Context, db *gorm.DB, event DomainEvent) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	return m.service.DomainAppender()(ctx, db, event)
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
