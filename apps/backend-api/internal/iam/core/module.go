package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	infraaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
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
	lockPrincipal  func(context.Context, *gorm.DB, guid.ID) error

	// foreign is the one holder of the table names other modules publish to the
	// tables core JOINS but does not own. Every reader core hands out shares it,
	// so the connectors below bind once for the whole module.
	foreign foreignTables

	// systemMenus is the default system menu; modules that own a system page
	// contribute it here, and the repository seeds and upgrades from it.
	systemMenus   SystemMenus
	contributions menuContributions
	menuSync      menuSyncRuntime

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
	// The one shared registry of the process: published through the registrar,
	// extended by business modules through Registry.Contribute, and read by
	// every authorization consumer core hands out.
	registry := authz.DefaultRegistry()
	instance := &Module{
		config:    config,
		runtime:   runtime,
		registrar: authz.NewUnboundRegistrar(registry),
	}
	// Every reader that JOINs a table core does not own shares the module's one
	// holder, so the connector the owner calls binds the whole module at once.
	instance.checker = &Authorizer{foreign: &instance.foreign}
	instance.members = NewMembershipChecker(runtime)
	instance.members.foreign = &instance.foreign
	guard := NewAdministratorGuard(runtime)
	guard.foreign = &instance.foreign
	guard.registry = registry
	instance.administrators = guard
	return instance
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, infraaudit.Name, jobs.Name, events.Name, idempotency.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[ActiveMemberChecker](services, ActiveMemberChecker(m.members)); err != nil {
		return fmt.Errorf("provide IAM active member checker: %w", err)
	}
	if err := m.provideProjectMembership(services); err != nil {
		return err
	}
	if err := module.Provide[PrincipalLockConnector](services, PrincipalLockConnector{
		Bind: func(lock func(context.Context, *gorm.DB, guid.ID) error) error {
			if lock == nil {
				return ErrUnavailable
			}
			m.lockPrincipal = lock
			return nil
		},
	}); err != nil {
		return fmt.Errorf("provide IAM principal lock connector: %w", err)
	}
	if err := module.Provide[PrincipalPlatformAccessGuard](services, func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		if tx == nil || id <= 0 {
			return ErrInvalidArgument
		}
		// The guard measures at the moment it runs. The deterministic path,
		// ErasePrincipalData, takes its caller's clock and passes it down; this
		// connector is called by a module that has none of its own.
		return requireNoPrincipalPlatformAccess(
			ctx, tx, m.runtime.TablePrefix(), id, m.registrar.Registry(),
			time.Now().UTC().Truncate(time.Millisecond),
		)
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
	if err := module.Provide[guid.RouteAuthenticator](services, m.registrar); err != nil {
		return fmt.Errorf("provide GUID route authenticator: %w", err)
	}
	if err := module.Provide[*SystemMenus](services, &m.systemMenus); err != nil {
		return fmt.Errorf("provide IAM system menus: %w", err)
	}
	if err := module.Provide[AuthorizationEvaluator](services, m.checker); err != nil {
		return fmt.Errorf("provide IAM authorization evaluator: %w", err)
	}
	if err := module.Provide[PlatformPermissionOn](services, PlatformPermissionOn(func(ctx context.Context, tx *gorm.DB, code string, subject guid.ID) (bool, error) {
		if m == nil || m.service == nil {
			return false, ErrUnavailable
		}
		return m.service.CheckPlatformOn(ctx, tx, code, subject)
	})); err != nil {
		return fmt.Errorf("provide IAM transaction system authorization: %w", err)
	}
	entityOwner := EntityOwnerResolver(func(ctx context.Context, entityID guid.ID) (authz.ResourceOwner, bool, error) {
		if m == nil || m.service == nil || m.service.repo == nil {
			return authz.ResourceOwner{}, false, ErrUnavailable
		}
		return m.service.resolveEntityOwner(ctx, entityID)
	})
	if err := module.Provide[EntityOwnerResolver](services, entityOwner); err != nil {
		return fmt.Errorf("provide IAM entity owner resolver: %w", err)
	}
	if err := m.provideEntityConstraint(services); err != nil {
		return fmt.Errorf("provide IAM transaction entity authorization: %w", err)
	}
	if err := module.Provide[TenantConstraint](services, m.checker.Constraint); err != nil {
		return fmt.Errorf("provide iam tenant constraint: %w", err)
	}
	if err := module.Provide[TenantConstraintOn](services, m.checker.TenantConstraintOn); err != nil {
		return fmt.Errorf("provide IAM transaction tenant authorization: %w", err)
	}
	if err := module.Provide[AuthorizationExplanation](services, m.checker.ExplainOn); err != nil {
		return fmt.Errorf("provide IAM authorization explanation: %w", err)
	}
	if err := module.Provide[AuthorizationSubjectsOn](services, m.checker.AuthorizationSubjectsOn); err != nil {
		return fmt.Errorf("provide IAM transaction authorization subjects: %w", err)
	}
	if err := module.Provide[AuthorizationReferencesOn](services, m.checker.AuthorizationReferencesOn); err != nil {
		return fmt.Errorf("provide IAM transaction authorization references: %w", err)
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
	if err := module.Provide[DirectoryTableNamesConnector](services, DirectoryTableNamesConnector{
		Bind: m.foreign.bindDirectory,
	}); err != nil {
		return fmt.Errorf("provide IAM directory table names connector: %w", err)
	}
	if err := module.Provide[PrincipalTableNamesConnector](services, PrincipalTableNamesConnector{
		Bind: m.foreign.bindPrincipal,
	}); err != nil {
		return fmt.Errorf("provide IAM principal table names connector: %w", err)
	}
	if err := module.Provide[AccessTableNames](services, PublishedAccessTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("provide IAM access table names: %w", err)
	}
	if err := module.Provide[EntityRoleBindingStore](services, m.entityRoleBindingStore()); err != nil {
		return fmt.Errorf("provide IAM entity role binding store: %w", err)
	}
	if err := module.Provide[EntityProvisioner](services, m.entityProvisioner()); err != nil {
		return fmt.Errorf("provide IAM entity provisioner: %w", err)
	}
	if err := module.Provide[RoleSeeder](services, m.roleSeeder()); err != nil {
		return fmt.Errorf("provide IAM role seeder: %w", err)
	}
	if err := m.provideBindingRevoker(services); err != nil {
		return err
	}
	if err := m.provideMenuContributions(services); err != nil {
		return err
	}
	return m.provideTeamRoster(services)
}

func (m *Module) InjectServices(services *module.Services) error {
	if err := m.bindJobsAdministration(services); err != nil {
		return err
	}
	if err := m.bindEventsAdministration(services); err != nil {
		return err
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve IAM id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve IAM audit appender: %w", err)
	}
	roots, err := module.Resolve[FileRootProvisioner](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve file root provisioner: %w", err)
	}
	rootContent, err := module.Resolve[FileRootContentChecker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve file root content checker: %w", err)
	}
	verifier, err := module.Resolve[authz.TokenVerifier](services)
	if err != nil {
		return fmt.Errorf("resolve authentication verifier: %w", err)
	}
	m.ids, m.appender = ids, appender
	repository := NewRepository(m.runtime, ids)
	repository.foreign = &m.foreign
	repository.systemMenus = &m.systemMenus
	repository.contributions = &m.contributions
	// Keep the provider-phase pointer stable; requests start only after wiring.
	*m.checker = *NewAuthorizerWithRegistry(m.runtime, m.registrar.Registry())
	m.checker.foreign = &m.foreign
	if err := m.registrar.Bind(verifier, m.checker, m.members); err != nil {
		return fmt.Errorf("bind authorization registrar: %w", err)
	}
	viewConnector, err := module.Resolve[authnext.AccountViewAuthorizerConnector](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve account view authorizer connector: %w", err)
	}
	if err == nil {
		if viewConnector.Bind == nil {
			return ErrUnavailable
		}
		if err := viewConnector.Bind(m.authorizeAccountView); err != nil {
			return fmt.Errorf("bind account view authorizer: %w", err)
		}
	}
	m.service = NewService(m.registrar.Registry(), repository, m.checker, appender)
	reservations, err := module.Resolve[EntityTypeReservations](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve reserved entity types: %w", err)
	}
	if err == nil {
		m.service.reservedEntityTypes = make(map[string]struct{})
		for _, kind := range reservations() {
			if !validEntityType(kind) {
				return fmt.Errorf("%w: reserved entity type %q", ErrInvalidArgument, kind)
			}
			m.service.reservedEntityTypes[kind] = struct{}{}
		}
	}
	m.service.roots = roots
	m.service.rootContent = rootContent
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
	m.service.lockPrincipal = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		if m.lockPrincipal == nil {
			return ErrUnavailable
		}
		return m.lockPrincipal(ctx, tx, id)
	}
	m.grants = NewRoleGrantStore(repository, m.administrators)
	return m.bindMenuSync(services)
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
	for _, path := range []string{
		"/tenants/{tenant_id}/roles", "/operating-platforms/{platform_id}/roles", "/roles/{role_id}",
		"/system/roles", "/system/roles/{role_id}", "/roles/{role_id}/data-scope", "/roles/{role_id}/members/{principal_id}",
	} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
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
		RoleGranted: func(ctx context.Context, db *gorm.DB, tenantID, roleID, principalID guid.ID, now time.Time) (bool, error) {
			if m == nil || m.grants.RoleGranted == nil {
				return false, ErrUnavailable
			}
			return m.grants.RoleGranted(ctx, db, tenantID, roleID, principalID, now)
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
