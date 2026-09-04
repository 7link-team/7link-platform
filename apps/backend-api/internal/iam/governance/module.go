package governance

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	governancei18n "7link.com/platform/internal/iam/governance/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

var ErrUnavailable = errors.New("governance service unavailable")

type Config struct {
	module.Config
}

type Module struct {
	config          Config
	service         *Service
	runtime         *appenv.Runtime
	registrar       *authz.Registrar
	ids             guid.Source
	appender        audit.Appender
	grants          core.RoleGrantStore
	administrators  core.AdministratorGuard
	declarationOnly bool
}

func NewModule(runtime *appenv.Runtime, registrar *authz.Registrar, config Config) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() {
		panic("governance module requires runtime and authz registrar")
	}
	return &Module{runtime: runtime, registrar: registrar, config: config}
}

func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("governance declaration module requires a declaration-only authz registrar")
	}
	return &Module{registrar: registrar, config: Config{Config: module.Config{Enabled: true}}, declarationOnly: true}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name}
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve governance id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve governance audit appender: %w", err)
	}
	grants, err := module.Resolve[core.RoleGrantStore](services)
	if err != nil {
		return fmt.Errorf("resolve governance role grant store: %w", err)
	}
	administrators, err := module.Resolve[core.AdministratorGuard](services)
	if err != nil {
		return fmt.Errorf("resolve governance administrator guard: %w", err)
	}
	m.ids, m.appender, m.grants, m.administrators = ids, appender, grants, administrators
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := governancei18n.Register(); err != nil {
		return fmt.Errorf("register governance i18n: %w", err)
	}
	if m == nil {
		return errors.New("initialize governance: module is required")
	}
	if m.declarationOnly {
		return nil
	}
	if m.runtime == nil || m.registrar == nil || m.ids == nil || m.appender == nil || m.administrators == nil ||
		m.grants.Grant == nil || m.grants.Revoke == nil || m.grants.RemovePermanent == nil ||
		m.grants.RemoveGroupMembership == nil || m.grants.RemovePositionMembership == nil || m.grants.RemoveEntityRoleBinding == nil {
		return errors.New("initialize governance: runtime, authz registrar, id generator, audit appender, role grant store, and administrator guard are required")
	}
	m.service = NewService(m.runtime, m.appender, m.grants, m.ids)
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
		return errors.New("register governance: module is unavailable")
	}
	if !m.declarationOnly && m.service == nil {
		return errors.New("register governance: service is unavailable")
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
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
