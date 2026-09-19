package organization

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// ErrUnavailable reports that the organization module has not been
// initialized with writable services.
var ErrUnavailable = errors.New("organization service unavailable")

// Module owns organization services, HTTP operations, locale behavior, and
// the GroupProvisioner capability consumed by provisioning.
type Config struct {
	module.Config
}

type Module struct {
	config           Config
	service          *Service
	tenants          *TenantService
	positions        *PositionService
	groups           *GroupService
	invitations      *InvitationService
	runtime          *appenv.Runtime
	registrar        *authz.Registrar
	members          core.ActiveMemberChecker
	administrators   core.AdministratorGuard
	credentials      authn.InvitationCredentials
	createPrincipal  identity.InvitationPrincipalCreator
	ids              guid.Source
	appender         audit.Appender
	bootstrap        core.TenantBootstrapper
	roots            core.FileRootProvisioner
	rootContent      core.FileRootContentChecker
	owners           identity.TenantOwnerLookup
	directorySources core.DirectoryAuthorizationSourceConnector
	// foreign is the one holder of the table names core published to
	// organization. Every service's repository reads through it, so a name
	// that was never published fails the statement instead of reaching the
	// database.
	foreign foreignTables
}

// NewModule builds a runnable organization module. Shared capabilities are
// resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("organization module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name, authn.Name, identity.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[GroupProvisioner](services, m); err != nil {
		return fmt.Errorf("provide organization group provisioner: %w", err)
	}
	if err := module.Provide[TenantLookup](services, m.tenantLookup()); err != nil {
		return fmt.Errorf("provide organization tenant lookup: %w", err)
	}
	if err := module.Provide[DirectoryTableNames](services, PublishedDirectoryTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("provide organization directory table names: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve organization authorization registrar: %w", err)
	}
	members, err := module.Resolve[core.ActiveMemberChecker](services)
	if err != nil {
		return fmt.Errorf("resolve organization active member checker: %w", err)
	}
	administrators, err := module.Resolve[core.AdministratorGuard](services)
	if err != nil {
		return fmt.Errorf("resolve organization administrator guard: %w", err)
	}
	credentials, err := module.Resolve[authn.InvitationCredentials](services)
	if err != nil {
		return fmt.Errorf("resolve organization invitation credentials: %w", err)
	}
	createPrincipal, err := module.Resolve[identity.InvitationPrincipalCreator](services)
	if err != nil {
		return fmt.Errorf("resolve organization invitation principal creator: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve organization id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve organization audit appender: %w", err)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](services)
	if err != nil {
		return fmt.Errorf("resolve organization tenant bootstrapper: %w", err)
	}
	roots, err := module.Resolve[core.FileRootProvisioner](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve organization file root provisioner: %w", err)
	}
	rootContent, err := module.Resolve[core.FileRootContentChecker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve organization file root content checker: %w", err)
	}
	owners, err := module.Resolve[identity.TenantOwnerLookup](services)
	if err != nil {
		return fmt.Errorf("resolve organization tenant owner lookup: %w", err)
	}
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve organization access tables: %w", err)
	}
	if err := m.foreign.bind(accessTables); err != nil {
		return fmt.Errorf("bind organization access tables: %w", err)
	}
	directorySources, err := module.Resolve[core.DirectoryAuthorizationSourceConnector](services)
	if err != nil {
		return fmt.Errorf("resolve organization directory authorization source connector: %w", err)
	}
	if directorySources.Bind == nil {
		return errors.New("resolve organization directory authorization source connector: bind is required")
	}
	if err := directorySources.Bind(NewDirectoryAuthorizationSourceStore(m.runtime)); err != nil {
		return fmt.Errorf("bind organization directory authorization sources: %w", err)
	}
	// Each consumer declares the fields it JOINs and organization fills exactly
	// those. The names all come from the module's own published struct, so a
	// consumer cannot be handed a name organization builds a second way.
	tenants := PublishedDirectoryTableNames(m.runtime.TablePrefix()).Tenants
	directoryTables, err := module.Resolve[core.DirectoryTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve organization directory table names connector: %w", err)
	}
	if directoryTables.Bind == nil {
		return errors.New("resolve organization directory table names connector: bind is required")
	}
	if err := directoryTables.Bind(CoreDirectoryTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("bind organization directory table names: %w", err)
	}
	authnDirectoryTables, err := module.Resolve[authn.DirectoryTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve authn directory table names connector: %w", err)
	}
	if authnDirectoryTables.Bind == nil {
		return errors.New("resolve authn directory table names connector: bind is required")
	}
	if err := authnDirectoryTables.Bind(authn.DirectoryTableNames{Tenants: tenants}); err != nil {
		return fmt.Errorf("bind authn directory table names: %w", err)
	}
	identityDirectoryTables, err := module.Resolve[identity.DirectoryTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve identity directory table names connector: %w", err)
	}
	if identityDirectoryTables.Bind == nil {
		return errors.New("resolve identity directory table names connector: bind is required")
	}
	if err := identityDirectoryTables.Bind(identity.DirectoryTableNames{Tenants: tenants}); err != nil {
		return fmt.Errorf("bind identity directory table names: %w", err)
	}
	m.registrar = registrar
	m.bootstrap, m.roots, m.rootContent, m.owners = bootstrap, roots, rootContent, owners
	m.members, m.administrators = members, administrators
	m.credentials, m.createPrincipal = credentials, createPrincipal
	m.ids, m.appender = ids, appender
	m.directorySources = directorySources
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := organizationi18n.Register(); err != nil {
		return fmt.Errorf("register organization i18n: %w", err)
	}
	if m == nil {
		return errors.New("initialize organization: module is required")
	}
	if m.runtime == nil || m.registrar == nil || m.members == nil || m.administrators == nil ||
		m.credentials == nil || m.createPrincipal == nil || m.ids == nil || m.appender == nil {
		return errors.New("initialize organization: runtime, authz registrar, member checker, administrator guard, invitation credentials, principal creator, id generator, and audit appender are required")
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return errors.New("initialize organization: the core module must publish the table names organization joins")
	}
	m.service = NewService(m.runtime, m.appender, m.ids)
	m.tenants = NewTenantService(m.runtime, m.appender, m.ids, m.bootstrap, m.owners)
	m.service.roots, m.tenants.roots = m.roots, m.roots
	m.service.rootContent = m.rootContent
	m.positions = NewPositionService(m.runtime, m.appender, m.members, m.administrators, m.ids)
	m.groups = NewGroupService(m.runtime, m.appender, m.members, m.administrators, m.ids)
	m.invitations = NewInvitationService(m.runtime, m.appender, m.ids, m.credentials, m.createPrincipal)
	for _, repo := range []*Repository{
		m.service.repo, m.tenants.repo, m.positions.repo, m.groups.repo, m.invitations.repo,
	} {
		repo.foreign = &m.foreign
	}
	return nil
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil ||
		m.tenants == nil || m.positions == nil || m.groups == nil || m.invitations == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register organization: module is unavailable")
	}
	if m.service == nil || m.tenants == nil || m.positions == nil || m.groups == nil || m.invitations == nil {
		return errors.New("register organization: services are unavailable")
	}
	RegisterHTTP(api, m.service, m.tenants, m.positions, m.groups, m.invitations, m.registrar)
	return nil
}

func (m *Module) CreateProvisionedTo(ctx context.Context, db *gorm.DB, tenantID guid.ID, input ProvisionedGroupInput) (Group, error) {
	if m == nil || m.groups == nil {
		return Group{}, ErrUnavailable
	}
	return m.groups.CreateProvisionedTo(ctx, db, tenantID, input)
}

func (m *Module) ReplaceProvisionedTo(ctx context.Context, db *gorm.DB, tenantID, id guid.ID, input ProvisionedGroupInput) (Group, error) {
	if m == nil || m.groups == nil {
		return Group{}, ErrUnavailable
	}
	return m.groups.ReplaceProvisionedTo(ctx, db, tenantID, id, input)
}

func (m *Module) DisableProvisionedTo(ctx context.Context, db *gorm.DB, tenantID, id guid.ID) (Group, error) {
	if m == nil || m.groups == nil {
		return Group{}, ErrUnavailable
	}
	return m.groups.DisableProvisionedTo(ctx, db, tenantID, id)
}

func (m *Module) Get(ctx context.Context, tenantID, id guid.ID) (Group, error) {
	if m == nil || m.groups == nil {
		return Group{}, ErrUnavailable
	}
	return m.groups.Get(ctx, tenantID, id)
}

func (m *Module) ListMembers(ctx context.Context, tenantID, id guid.ID) ([]GroupMember, error) {
	if m == nil || m.groups == nil {
		return nil, ErrUnavailable
	}
	return m.groups.ListMembers(ctx, tenantID, id)
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
	_ GroupProvisioner       = (*Module)(nil)
)
