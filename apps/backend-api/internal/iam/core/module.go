package core

import (
	"context"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

// Module owns core IAM authorization, membership, menu, entity, relationship,
// HTTP, locale, and migration behavior.
type Module struct {
	runtime         *appenv.Runtime
	service         *Service
	registrar       *authz.Registrar
	declarationOnly bool
}

// NewModule builds a runnable core IAM module. Cross-module construction stays
// in internal/app; this module only composes its own repository and service.
func NewModule(
	runtime *appenv.Runtime,
	registrar *authz.Registrar,
	checker AuthorizationEvaluator,
	appender audit.Appender,
	ids guid.Source,
) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || checker == nil || appender == nil || ids == nil {
		panic("iam module requires runtime, authz registrar, authorizer, audit appender, and id generator")
	}
	repository := NewRepository(runtime, ids)
	return &Module{
		runtime: runtime, service: NewService(registrar.Registry(), repository, checker, appender),
		registrar: registrar,
	}
}

// NewDeclarationOnlyModule builds the route-declaration form used to assemble
// and validate the complete OpenAPI document without runtime dependencies.
func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("iam declaration module requires a declaration-only authz registrar")
	}
	return &Module{
		service: newDeclarationService(registrar.Registry()), registrar: registrar,
		declarationOnly: true,
	}
}

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name}
}

func (*Module) Initialize(context.Context) error {
	if err := corei18n.Register(); err != nil {
		return fmt.Errorf("register iam i18n: %w", err)
	}
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
	if m == nil || m.service == nil || m.registrar == nil {
		return fmt.Errorf("register iam: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.Depender      = (*Module)(nil)
	_ module.Initializer   = (*Module)(nil)
	_ module.HealthChecker = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)
