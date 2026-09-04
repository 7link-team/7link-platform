package core

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Module owns core IAM authorization, membership, menu, entity, relationship,
// HTTP, locale, and migration behavior.
type Config struct {
	module.Config
}

type Module struct {
	config          Config
	runtime         *appenv.Runtime
	service         *Service
	registrar       *authz.Registrar
	checker         AuthorizationEvaluator
	members         *MembershipChecker
	administrators  AdministratorGuard
	grants          RoleGrantStore
	ids             guid.Source
	appender        audit.Appender
	declarationOnly bool
}

// NewModule builds a runnable core IAM module. Cross-module construction stays
// in internal/app; this module only composes its own repository and service.
func NewModule(
	runtime *appenv.Runtime,
	registrar *authz.Registrar,
	checker AuthorizationEvaluator,
	config Config,
) *Module {
	if runtime == nil || registrar == nil || registrar.Registry() == nil || registrar.IsDeclarationOnly() || checker == nil {
		panic("iam module requires runtime, authz registrar, and authorizer")
	}
	module := &Module{
		config:  config,
		runtime: runtime, registrar: registrar, checker: checker,
		members: NewMembershipChecker(runtime), administrators: NewAdministratorGuard(runtime),
	}
	return module
}

// NewDeclarationOnlyModule builds the route-declaration form used to assemble
// and validate the complete OpenAPI document without runtime dependencies.
func NewDeclarationOnlyModule(registrar *authz.Registrar) *Module {
	if registrar == nil || registrar.Registry() == nil || !registrar.IsDeclarationOnly() {
		panic("iam declaration module requires a declaration-only authz registrar")
	}
	return &Module{
		service: newDeclarationService(registrar.Registry()), registrar: registrar,
		config: Config{Config: module.Config{Enabled: true}}, declarationOnly: true,
	}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if m != nil && m.declarationOnly {
		return nil
	}
	if err := module.Provide[ActiveMemberChecker](services, ActiveMemberChecker(m.members)); err != nil {
		return fmt.Errorf("provide IAM active member checker: %w", err)
	}
	if err := module.Provide[AdministratorGuard](services, m.administrators); err != nil {
		return fmt.Errorf("provide IAM administrator guard: %w", err)
	}
	if err := module.Provide[RoleGrantStore](services, m.roleGrantStore()); err != nil {
		return fmt.Errorf("provide IAM role grant store: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve IAM id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve IAM audit appender: %w", err)
	}
	m.ids, m.appender = ids, appender
	repository := NewRepository(m.runtime, ids)
	m.service = NewService(m.registrar.Registry(), repository, m.checker, appender)
	m.service.administrators = m.administrators
	m.grants = NewRoleGrantStore(repository, m.administrators)
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := corei18n.Register(); err != nil {
		return fmt.Errorf("register iam i18n: %w", err)
	}
	if m == nil || (!m.declarationOnly && (m.service == nil || m.ids == nil || m.appender == nil)) {
		return fmt.Errorf("initialize IAM: id generator and audit appender are required")
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

// roleGrantStore is provided before injection, so each closure resolves the
// real repository-backed function at call time after Wire has completed.
func (m *Module) roleGrantStore() RoleGrantStore {
	return RoleGrantStore{
		Grant: func(ctx context.Context, db *gorm.DB, tenantID, requestID, roleID, principalID, grantedBy guid.ID, startsAt, endsAt time.Time) error {
			if m == nil || m.grants.Grant == nil {
				return ErrUnavailable
			}
			return m.grants.Grant(ctx, db, tenantID, requestID, roleID, principalID, grantedBy, startsAt, endsAt)
		},
		Revoke: func(ctx context.Context, db *gorm.DB, tenantID, requestID guid.ID) (bool, error) {
			if m == nil || m.grants.Revoke == nil {
				return false, ErrUnavailable
			}
			return m.grants.Revoke(ctx, db, tenantID, requestID)
		},
		RemovePermanent: func(ctx context.Context, db *gorm.DB, tenantID, roleID, principalID guid.ID, createdAt time.Time) (bool, error) {
			if m == nil || m.grants.RemovePermanent == nil {
				return false, ErrUnavailable
			}
			return m.grants.RemovePermanent(ctx, db, tenantID, roleID, principalID, createdAt)
		},
		RemoveGroupMembership: func(ctx context.Context, db *gorm.DB, tenantID, groupID, principalID guid.ID) (bool, error) {
			if m == nil || m.grants.RemoveGroupMembership == nil {
				return false, ErrUnavailable
			}
			return m.grants.RemoveGroupMembership(ctx, db, tenantID, groupID, principalID)
		},
		RemovePositionMembership: func(ctx context.Context, db *gorm.DB, tenantID, positionID, principalID guid.ID) (bool, error) {
			if m == nil || m.grants.RemovePositionMembership == nil {
				return false, ErrUnavailable
			}
			return m.grants.RemovePositionMembership(ctx, db, tenantID, positionID, principalID)
		},
		RemoveEntityRoleBinding: func(ctx context.Context, db *gorm.DB, tenantID, entityID, roleID, principalID guid.ID) (bool, error) {
			if m == nil || m.grants.RemoveEntityRoleBinding == nil {
				return false, ErrUnavailable
			}
			return m.grants.RemoveEntityRoleBinding(ctx, db, tenantID, entityID, roleID, principalID)
		},
	}
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
