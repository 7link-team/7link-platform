package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
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

	// RequirePlatformAdministrator makes readiness fail until the platform has
	// at least one administrator. A freshly migrated database can serve no
	// platform operation, so reporting it ready would hand operators a green
	// probe for an unusable deployment.
	RequirePlatformAdministrator bool
}

type Module struct {
	config         Config
	runtime        *appenv.Runtime
	service        *Service
	repository     *Repository
	registrar      *authz.Registrar
	checker        *Authorizer
	members        *MembershipChecker
	administrators AdministratorGuard
	grants         RoleGrantStore
	ids            guid.Source
	appender       audit.Appender

	// administratorPresent latches the readiness gate. The last-administrator
	// guard means the count never returns to zero, so once it is satisfied the
	// probe stops paying for a union query on every request.
	administratorPresent atomic.Bool
}

// NewModule builds the core IAM module and its stable authorization gateway.
// Runtime collaborators are connected later through module.Services.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("iam module requires runtime")
	}
	registry := authz.DefaultRegistry()
	instance := &Module{
		config:         config,
		runtime:        runtime,
		registrar:      authz.NewUnboundRegistrar(registry),
		checker:        &Authorizer{},
		members:        NewMembershipChecker(runtime),
		administrators: NewAdministratorGuard(runtime),
	}
	return instance
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[ActiveMemberChecker](services, ActiveMemberChecker(m.members)); err != nil {
		return fmt.Errorf("provide IAM active member checker: %w", err)
	}
	if err := module.Provide[PrincipalPlatformAccessGuard](services, func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		if tx == nil || id <= 0 {
			return ErrInvalidArgument
		}
		return requireNoPrincipalPlatformAccess(ctx, tx, m.runtime.TablePrefix(), id)
	}); err != nil {
		return err
	}
	if err := module.Provide[AdministratorGuard](services, m.administrators); err != nil {
		return fmt.Errorf("provide IAM administrator guard: %w", err)
	}
	if err := module.Provide[RoleGrantStore](services, m.roleGrantStore()); err != nil {
		return fmt.Errorf("provide IAM role grant store: %w", err)
	}
	if err := module.Provide[PlatformAdministratorStore](services, m.platformAdministratorStore()); err != nil {
		return fmt.Errorf("provide IAM platform administrator store: %w", err)
	}
	if err := module.Provide[TenantBootstrapper](services, m.tenantBootstrapper()); err != nil {
		return fmt.Errorf("provide IAM tenant bootstrapper: %w", err)
	}
	if err := module.Provide[*authz.Registrar](services, m.registrar); err != nil {
		return fmt.Errorf("provide authorization registrar: %w", err)
	}
	if err := module.Provide[AuthorizationEvaluator](services, m.checker); err != nil {
		return fmt.Errorf("provide IAM authorization evaluator: %w", err)
	}
	if err := module.Provide[MemberDepartmentReader](services, m.members); err != nil {
		return fmt.Errorf("provide IAM member department reader: %w", err)
	}
	if err := module.Provide[MemberTenantResolver](services, m.members.ResolveTenant); err != nil {
		return fmt.Errorf("provide IAM member tenant resolver: %w", err)
	}
	if err := module.Provide[DirectoryAuthorizationSourceConnector](services, DirectoryAuthorizationSourceConnector{
		Bind: m.checker.BindDirectoryAuthorizationSources,
	}); err != nil {
		return fmt.Errorf("provide IAM directory authorization source connector: %w", err)
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
	verifier, err := module.Resolve[authz.TokenVerifier](services)
	if err != nil {
		return fmt.Errorf("resolve authentication verifier: %w", err)
	}
	m.ids, m.appender = ids, appender
	repository := NewRepository(m.runtime, ids)
	// Keep the provider-phase pointer stable; requests start only after wiring.
	*m.checker = *NewAuthorizerWithRegistry(m.runtime, m.registrar.Registry())
	if err := m.registrar.Bind(verifier, m.checker, m.members); err != nil {
		return fmt.Errorf("bind authorization registrar: %w", err)
	}
	m.service = NewService(m.registrar.Registry(), repository, m.checker, appender)
	m.service.administrators = m.administrators
	m.service.privacyBrowser, err = module.Resolve[authnext.BrowserSessionSecurity](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.service.reauthenticate, err = module.Resolve[authnext.ReauthenticationConsumer](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.service.revokePersonalTokens, err = module.Resolve[TenantPersonalTokenRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.service.revokeOAuth, err = module.Resolve[TenantOAuthRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.repository = repository
	m.grants = NewRoleGrantStore(repository, m.administrators)
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := corei18n.Register(); err != nil {
		return fmt.Errorf("register iam i18n: %w", err)
	}
	if m == nil || m.service == nil || m.ids == nil || m.appender == nil {
		return fmt.Errorf("initialize IAM: id generator and audit appender are required")
	}
	return nil
}

func (m *Module) CheckHealth(ctx context.Context) error {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	if !m.config.RequirePlatformAdministrator || m.administratorPresent.Load() {
		return nil
	}
	administrators, err := m.service.repo.ListPlatformAdministrators(ctx)
	if err != nil {
		return err
	}
	if len(administrators) == 0 {
		return ErrPlatformAdministratorRequired
	}
	m.administratorPresent.Store(true)
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.service == nil || m.registrar == nil {
		return fmt.Errorf("register iam: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

// platformAdministratorStore is provided before injection, so each closure
// resolves the real repository at call time after Wire has completed.
func (m *Module) platformAdministratorStore() PlatformAdministratorStore {
	return PlatformAdministratorStore{
		List: func(ctx context.Context, db *gorm.DB) ([]PlatformAdministrator, error) {
			if m == nil || m.repository == nil {
				return nil, ErrUnavailable
			}
			return m.repository.ListPlatformAdministratorsOn(ctx, db)
		},
		GrantFull: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (bool, error) {
			if m == nil || m.repository == nil {
				return false, ErrUnavailable
			}
			return m.repository.GrantFullPlatformAdministratorOn(ctx, db, principalID)
		},
	}
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
