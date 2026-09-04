package identity

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// ErrUnavailable reports that the identity module cannot serve a request
// because it has no initialized service.
var ErrUnavailable = errors.New("identity service unavailable")

// Module owns principals, service accounts, their HTTP operations, locale
// bundle, and migration source. It provides the identity capabilities SCIM,
// federation, and invitation acceptance depend on.
type Config struct {
	module.Config
}

type Module struct {
	*Service
	config         Config
	runtime        *appenv.Runtime
	registrar      *authz.Registrar
	administrators core.AdministratorGuard
	ids            guid.Source
	appender       audit.Appender
}

// NewModule builds the identity module from its runtime and authorization
// registrar. Shared capabilities are resolved during module wiring. The source
// module has no declaration-only constructor, so this module is not given one
// either.
func NewModule(
	runtime *appenv.Runtime,
	registrar *authz.Registrar,
	config Config,
) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() {
		panic("identity module requires runtime and authz registrar")
	}
	return &Module{runtime: runtime, registrar: registrar, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[IdentityProvisioner](services, m); err != nil {
		return fmt.Errorf("provide identity provisioner: %w", err)
	}
	// The service exists only after Initialize, so the invitation capability is
	// a closure that resolves it per call instead of a method value bound now.
	creator := InvitationPrincipalCreator(func(
		ctx context.Context,
		db *gorm.DB,
		tenantID guid.ID,
		loginName, password, displayName, email string,
	) (guid.ID, error) {
		if m == nil || m.Service == nil {
			return 0, ErrUnavailable
		}
		return m.Service.CreateInvitedPrincipal(ctx, db, tenantID, loginName, password, displayName, email)
	})
	if err := module.Provide[InvitationPrincipalCreator](services, creator); err != nil {
		return fmt.Errorf("provide identity invitation principal creator: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve identity id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve identity audit appender: %w", err)
	}
	administrators, err := module.Resolve[core.AdministratorGuard](services)
	if err != nil {
		return fmt.Errorf("resolve identity administrator guard: %w", err)
	}
	m.ids = ids
	m.appender = appender
	m.administrators = administrators
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := identityi18n.Register(); err != nil {
		return fmt.Errorf("register identity i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil || m.administrators == nil {
		return errors.New(
			"initialize identity: runtime, id generator, audit appender, and administrator guard are required",
		)
	}
	m.Service = NewService(m.runtime, m.appender, m.administrators, m.ids)
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
		return errors.New("register identity: service is unavailable")
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
	_ IdentityProvisioner    = (*Module)(nil)
)
