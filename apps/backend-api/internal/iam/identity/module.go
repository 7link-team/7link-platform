package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
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
	config              Config
	runtime             *appenv.Runtime
	registrar           *authz.Registrar
	administrators      core.AdministratorGuard
	fields              authz.FieldPermissionChecker
	avatarPermissions   authz.PermissionChecker
	resourceFiles       *files.AccountResourceFiles
	legacyImporter      files.LegacyImporter
	ids                 guid.Source
	appender            audit.Appender
	anonymizer          PrincipalAnonymizer
	erasureRecorder     audit.ErasureRecorder
	privacyBrowser      authnext.BrowserSessionSecurity
	reauthenticate      authnext.ReauthenticationConsumer
	closeCredentials    PrincipalCredentialsRevoker
	closeOAuth          PrincipalOAuthRevoker
	platformAccessGuard core.PrincipalPlatformAccessGuard
	revokeTenantTokens  core.TenantPersonalTokenRevoker
	revokeTenantOAuth   core.TenantOAuthRevoker

	// foreign is the one holder of the table names the modules that own them
	// publish to identity. The connectors below bind it, and the repository the
	// module builds shares it.
	foreign foreignTables
	// credentials is authn's capability for the tables authn owns. identity
	// resolves it optionally: a deployment without authn has no credentials to
	// write, and the repository refuses rather than writing them itself.
	credentials PrincipalCredentialStore
}

// RegistrationPrincipalCreator creates a pending principal and its credential
// inside the caller-owned authentication registration transaction.
type RegistrationPrincipalCreator func(
	context.Context,
	*gorm.DB,
	string,
	string,
	string,
	time.Time,
) (guid.ID, error)

// DirectRegistrationPrincipalCreator creates an unverified account without a
// mailbox activation requirement when deployment email verification is disabled.
type DirectRegistrationPrincipalCreator func(context.Context, *gorm.DB, string, string, string, string, time.Time) (guid.ID, error)

// NewModule builds the identity module from construction-owned inputs. Shared
// capabilities are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("identity module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name, files.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[IdentityProvisioner](services, m); err != nil {
		return fmt.Errorf("provide identity provisioner: %w", err)
	}
	if err := m.provideEnrollment(services); err != nil {
		return err
	}
	if err := m.provideAgentCredentialRotation(services); err != nil {
		return err
	}
	if err := module.Provide[NotificationCandidates](services, m.notificationCandidates); err != nil {
		return err
	}
	if err := m.provideAccountNotifications(services); err != nil {
		return err
	}
	validateAccount := ServiceAccountValidator(func(ctx context.Context, db *gorm.DB, tenantID, principalID guid.ID, now time.Time) (bool, error) {
		if m == nil || m.Service == nil {
			return false, ErrUnavailable
		}
		return m.Service.ValidateServiceAccount(ctx, db, tenantID, principalID, now)
	})
	if err := module.Provide[ServiceAccountValidator](services, validateAccount); err != nil {
		return fmt.Errorf("provide service account validator: %w", err)
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
	if err := module.Provide[InvitationMemberAcceptor](services, func(ctx context.Context, db *gorm.DB, tenant, principal guid.ID) error {
		if m == nil || m.Service == nil {
			return ErrUnavailable
		}
		return m.Service.AcceptInvitationMember(ctx, db, tenant, principal)
	}); err != nil {
		return fmt.Errorf("provide identity invitation member acceptor: %w", err)
	}
	registrationCreator := RegistrationPrincipalCreator(func(
		ctx context.Context,
		db *gorm.DB,
		email, passwordHash, displayName string,
		now time.Time,
	) (guid.ID, error) {
		if m == nil || m.Service == nil {
			return 0, ErrUnavailable
		}
		return m.Service.CreatePendingPrincipal(ctx, db, email, passwordHash, displayName, now, m.ids)
	})
	if err := module.Provide[RegistrationPrincipalCreator](services, registrationCreator); err != nil {
		return fmt.Errorf("provide identity registration principal creator: %w", err)
	}
	directCreator := DirectRegistrationPrincipalCreator(func(ctx context.Context, db *gorm.DB, login, email, hash, name string, now time.Time) (guid.ID, error) {
		if m == nil || m.Service == nil {
			return 0, ErrUnavailable
		}
		return m.Service.CreateUnverifiedRegistration(ctx, db, login, email, hash, name, now, m.ids)
	})
	if err := module.Provide[DirectRegistrationPrincipalCreator](services, directCreator); err != nil {
		return err
	}
	if err := module.Provide[BootstrapPrincipalStore](services, m.bootstrapPrincipalStore()); err != nil {
		return fmt.Errorf("provide identity bootstrap principal store: %w", err)
	}
	if err := module.Provide[TenantOwnerLookup](services, m.tenantOwnerLookup()); err != nil {
		return fmt.Errorf("provide identity tenant owner lookup: %w", err)
	}
	if err := module.Provide[PrincipalStateStore](services, m.principalStateStore()); err != nil {
		return fmt.Errorf("provide identity principal state store: %w", err)
	}
	if err := module.Provide[DirectoryTableNamesConnector](services, DirectoryTableNamesConnector{
		Bind: m.foreign.bindDirectory,
	}); err != nil {
		return fmt.Errorf("provide identity directory table names connector: %w", err)
	}
	if err := module.Provide[OAuthTableNamesConnector](services, OAuthTableNamesConnector{
		Bind: m.foreign.bindOAuth,
	}); err != nil {
		return fmt.Errorf("provide identity oauth table names connector: %w", err)
	}
	if err := module.Provide[PrincipalTableNames](services, PublishedPrincipalTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("provide identity principal table names: %w", err)
	}
	return m.provideAgentAvatar(services)
}

// bootstrapPrincipalStore is provided before injection, so each closure builds
// its repository at call time from collaborators Wire has since supplied. The
// platform bootstrap runs before Initialize, so it cannot go through Service.
func (m *Module) bootstrapPrincipalStore() BootstrapPrincipalStore {
	repository := func() (*Repository, error) {
		if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
			return nil, ErrUnavailable
		}
		repo := NewRepository(m.runtime, m.ids, m.appender)
		repo.resourceFiles = m.resourceFiles
		// The bootstrap runs after Wire, so the owners have published and the
		// module's holder already carries their names.
		repo.foreign = &m.foreign
		repo.credentials = m.credentials
		return repo, nil
	}
	return BootstrapPrincipalStore{
		Find: func(ctx context.Context, db *gorm.DB, loginName string) (Principal, bool, error) {
			repo, err := repository()
			if err != nil {
				return Principal{}, false, err
			}
			return repo.FindPrincipalByLoginOn(ctx, db, loginName)
		},
		Create: func(ctx context.Context, db *gorm.DB, input BootstrapPrincipalInput, now time.Time) (guid.ID, error) {
			repo, err := repository()
			if err != nil {
				return 0, err
			}
			return repo.CreateBootstrapPrincipalOn(ctx, db, input, now)
		},
	}
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve identity authorization registrar: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve identity id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve identity audit appender: %w", err)
	}
	erasureRecorder, err := module.Resolve[audit.ErasureRecorder](services)
	if err != nil {
		return fmt.Errorf("resolve audit erasure recorder: %w", err)
	}
	administrators, err := module.Resolve[core.AdministratorGuard](services)
	if err != nil {
		return fmt.Errorf("resolve identity administrator guard: %w", err)
	}
	fields, err := module.Resolve[core.AuthorizationEvaluator](services)
	if err != nil {
		return fmt.Errorf("resolve identity field permissions: %w", err)
	}
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve identity access tables: %w", err)
	}
	if err := m.foreign.bindAccess(accessTables); err != nil {
		return fmt.Errorf("bind identity access tables: %w", err)
	}
	principalTables, err := module.Resolve[core.PrincipalTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve identity principal table names connector: %w", err)
	}
	if principalTables.Bind == nil {
		return errors.New("resolve identity principal table names connector: bind is required")
	}
	if err := principalTables.Bind(CorePrincipalTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("bind identity principal table names: %w", err)
	}
	m.fields = fields
	m.avatarPermissions = fields
	labels, err := module.Resolve[audit.PrincipalLabelConnector](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err == nil {
		if labels.Bind == nil {
			return errors.New("resolve audit principal labels: bind is required")
		}
		if err := labels.Bind(func(ctx context.Context, tenant guid.ID, ids []guid.ID) (map[guid.ID]audit.PrincipalLabel, error) {
			if m.Service == nil {
				return nil, ErrUnavailable
			}
			return m.Service.auditPrincipalLabels(ctx, tenant, ids)
		}); err != nil {
			return fmt.Errorf("bind audit principal labels: %w", err)
		}
	}
	m.resourceFiles, err = module.Resolve[*files.AccountResourceFiles](services)
	if err != nil {
		return fmt.Errorf("resolve identity resource files: %w", err)
	}
	if err := registerAvatarResourcePurposes(m.resourceFiles, query.BindNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("register identity avatar file purposes: %w", err)
	}
	m.legacyImporter, err = module.Resolve[files.LegacyImporter](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.registrar = registrar
	m.ids = ids
	m.appender = appender
	m.erasureRecorder = erasureRecorder
	m.administrators = administrators
	m.privacyBrowser, err = module.Resolve[authnext.BrowserSessionSecurity](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.reauthenticate, err = module.Resolve[authnext.ReauthenticationConsumer](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.closeCredentials, err = module.Resolve[PrincipalCredentialsRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.credentials, err = module.Resolve[PrincipalCredentialStore](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	// A module that JOINs identity's tables provides this hook; identity pushes
	// its own names into it. Absent means nobody JOINs them in this deployment,
	// which is not identity's problem to report.
	principalState, err := module.Resolve[PrincipalStateReceiver](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err == nil {
		if principalState.Bind == nil {
			return errors.New("resolve identity principal state receiver: bind is required")
		}
		if err := principalState.Bind(m.principalStateStore()); err != nil {
			return fmt.Errorf("bind identity principal state: %w", err)
		}
	}
	publishedPrincipals, err := module.Resolve[PrincipalTableNamesReceiver](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err == nil {
		if publishedPrincipals.Bind == nil {
			return errors.New("resolve identity principal table names receiver: bind is required")
		}
		if err := publishedPrincipals.Bind(PublishedPrincipalTableNames(m.runtime.TablePrefix())); err != nil {
			return fmt.Errorf("bind identity principal table names: %w", err)
		}
	}
	m.closeOAuth, err = module.Resolve[PrincipalOAuthRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.revokeTenantTokens, err = module.Resolve[core.TenantPersonalTokenRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.revokeTenantOAuth, err = module.Resolve[core.TenantOAuthRevoker](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	principalLock, err := module.Resolve[core.PrincipalLockConnector](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err == nil {
		if principalLock.Bind == nil {
			return errors.New("identity principal lock connector requires bind")
		}
		if err := principalLock.Bind(m.principalStateStore().Lock); err != nil {
			return err
		}
	}
	m.platformAccessGuard, err = module.Resolve[core.PrincipalPlatformAccessGuard](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	anonymizer, err := module.Resolve[PrincipalAnonymizer](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return fmt.Errorf("resolve principal anonymizer: %w", err)
	}
	m.anonymizer = anonymizer
	return nil
}

func (m *Module) Initialize(ctx context.Context) error {
	if err := identityi18n.Register(); err != nil {
		return fmt.Errorf("register identity i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil || m.administrators == nil {
		return errors.New(
			"initialize identity: runtime, id generator, audit appender, and administrator guard are required",
		)
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return errors.New("initialize identity: the core, organization, and oauth modules must publish the table names identity joins")
	}
	m.Service = NewService(m.runtime, m.appender, m.administrators, m.ids, m.registrar.Registry())
	m.Service.repo.resourceFiles = m.resourceFiles
	// Wire has already run, so the owners have published; the service and every
	// repository it clones share the one holder the connectors bound.
	m.Service.repo.foreign = &m.foreign
	m.Service.repo.credentials = m.credentials
	m.Service.fields = m.fields
	m.Service.avatarPermissions = m.avatarPermissions
	m.Service.anonymizer = m.anonymizer
	m.Service.erasureRecorder = m.erasureRecorder
	m.Service.privacyBrowser, m.Service.reauthenticate = m.privacyBrowser, m.reauthenticate
	m.Service.closeCredentials, m.Service.closeOAuth, m.Service.platformAccessGuard = m.closeCredentials, m.closeOAuth, m.platformAccessGuard
	m.Service.revokeTenantTokens, m.Service.revokeTenantOAuth = m.revokeTenantTokens, m.revokeTenantOAuth
	if m.legacyImporter != nil {
		if err := m.Service.migrateAvatarStorage(ctx, m.legacyImporter); err != nil {
			return err
		}
	}
	if err := m.resourceFiles.MigrateLegacyAvatars(ctx, m.runtime.Database().Write(), time.Now()); err != nil {
		return fmt.Errorf("migrate legacy avatar references: %w", err)
	}
	return m.Service.migrateUngovernedAvatars(ctx)
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.Service == nil || m.Service.repo == nil || m.Service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

// Start backfills missing active-account avatars after GUID has started, not
// from a GET request and not during injection/initialization.
func (m *Module) Start(ctx context.Context) error {
	if m.Service == nil {
		return ErrUnavailable
	}
	return m.Service.backfillMissingAvatars(ctx)
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
