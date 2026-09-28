package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	authni18n "7link.com/platform/internal/iam/authn/i18n"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Module owns authentication credentials, sessions, factors, challenges,
// browser authentication HTTP operations, and their worker lifecycle.
type Module struct {
	*Service
	runtime               *appenv.Runtime
	config                Config
	registrationCreator   RegistrationPrincipalCreator
	ids                   guid.Source
	appender              audit.Appender
	memberTenant          core.MemberTenantResolver
	fields                authz.FieldPermissionChecker
	oauthGrants           OAuthGrantValidator
	personalPermissions   func(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error)
	personalRegistry      *authz.Registry
	recoveryEligibility   func(context.Context, *gorm.DB, guid.ID) error
	federatedProofTrust   func(context.Context, *gorm.DB, guid.ID, string, string) error
	accountViewAuthorizer AccountViewAuthorizer

	// foreign is the one holder of the table names the modules that own them
	// publish to authn. The connectors below bind it, and every repository the
	// module builds shares it.
	foreign foreignTables
	// principals is identity's own principal-state capability. identity pushes
	// it through the receiver authn provides, because authn is activated first
	// and so cannot resolve an identity type itself.
	principals identity.PrincipalStateStore
}

// Config separates module activation from the authentication service's
// operational settings while allowing the composition root to pass one value.
type Config struct {
	module.Config
	Service authnext.Config
}

// NewModule builds the authn module from construction-owned inputs. Shared
// capabilities are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("authn module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

// Enabled is evaluated before Initialize, so it must use construction-time
// configuration rather than the embedded service created during Initialize.
func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleDependencies() []string {
	return []string{platformi18n.Name, guid.Name, audit.Name, core.Name}
}

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[identity.PrincipalCredentialsRevoker](services, func(ctx context.Context, tx *gorm.DB, principalID guid.ID) error {
		if m.Service == nil {
			return authnext.ErrUnavailable
		}
		return m.Service.revokePrincipalCredentialsOn(ctx, tx, principalID)
	}); err != nil {
		return err
	}
	if err := module.Provide[AccountViewAuthorizerConnector](services, AccountViewAuthorizerConnector{Bind: func(check AccountViewAuthorizer) error {
		m.accountViewAuthorizer = check
		if m.Service != nil {
			m.Service.accountViewAuthorizer = check
		}
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide[core.TenantPersonalTokenRevoker](services, func(ctx context.Context, tx *gorm.DB, tenantID, principalID guid.ID) error {
		if m.Service == nil {
			return authnext.ErrUnavailable
		}
		return m.Service.revokeTenantPersonalTokensOn(ctx, tx, tenantID, principalID)
	}); err != nil {
		return err
	}
	if err := module.Provide[RecoveryEligibilityConnector](services, RecoveryEligibilityConnector{Bind: func(check func(context.Context, *gorm.DB, guid.ID) error) error {
		m.recoveryEligibility = check
		if m.Service != nil {
			m.Service.recoveryEligibility = check
		}
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide[FederatedProofTrustConnector](services, FederatedProofTrustConnector{Bind: func(check func(context.Context, *gorm.DB, guid.ID, string, string) error) error {
		m.federatedProofTrust = check
		if m.Service != nil {
			m.Service.federatedProofTrust = check
		}
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide[authnext.BrowserSessionSecurity](services, authnext.BrowserSessionSecurity{
		Authenticate: func(ctx context.Context, cookie string) (*authnext.Claims, error) {
			return m.Authenticate(ctx, "", cookie)
		},
		ValidateCSRF: func(method, origin, cookie string) error { return m.ValidateCSRF(method, origin, cookie, "") },
	}); err != nil {
		return err
	}
	if err := module.Provide[ReauthenticationConsumer](services, func(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader, proof, purpose, target string) error {
		return m.Service.ConsumeReauthenticationOn(ctx, tx, principalID, cookieHeader, proof, purpose, target)
	}); err != nil {
		return err
	}
	if err := module.Provide[FederatedReauthentication](services, FederatedReauthentication{
		ConsumeIndependentOn: func(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader, proof, purpose, target string) error {
			return m.Service.ConsumeIndependentReauthenticationOn(ctx, tx, principalID, cookieHeader, proof, purpose, target)
		},
		Inspect: func(ctx context.Context, cookieHeader, proof string) (ReauthenticationBinding, error) {
			if m.Service == nil {
				return ReauthenticationBinding{}, authnext.ErrUnavailable
			}
			return m.Service.inspectFederatedReauthentication(ctx, cookieHeader, proof)
		},
		VerifyOn: func(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader, hash, linkID, fingerprint string) error {
			return m.Service.verifyFederatedReauthenticationOn(ctx, tx, principalID, cookieHeader, hash, linkID, fingerprint)
		},
		ValidateSessionOn: func(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader string) (int64, error) {
			if m.Service == nil || tx == nil {
				return 0, authnext.ErrUnavailable
			}
			return m.Service.lockReauthenticationSession(ctx, m.Service.repo.withDB(tx, true), principalID, currentSessionHash(cookieHeader, m.Service.web.CookieName))
		},
		LocalMethodsOn: func(ctx context.Context, tx *gorm.DB, principalID guid.ID) (int, error) {
			return m.Service.localLoginMethodsOn(ctx, tx, principalID)
		},
	}); err != nil {
		return err
	}
	if err := module.Provide[WebService](services, m); err != nil {
		return fmt.Errorf("provide authn web service: %w", err)
	}
	if err := module.Provide[InvitationCredentials](services, m); err != nil {
		return fmt.Errorf("provide authn invitation credentials: %w", err)
	}
	if err := module.Provide[authz.TokenVerifier](services, m); err != nil {
		return fmt.Errorf("provide authn token verifier: %w", err)
	}
	if err := module.Provide[BootstrapCredentialStore](services, m.bootstrapCredentialStore()); err != nil {
		return fmt.Errorf("provide authn bootstrap credential store: %w", err)
	}
	if err := module.Provide[SecretRewrapper](services, m); err != nil {
		return fmt.Errorf("provide authn secret rewrapper: %w", err)
	}
	if err := module.Provide[DirectoryTableNamesConnector](services, DirectoryTableNamesConnector{
		Bind: m.foreign.bindDirectory,
	}); err != nil {
		return fmt.Errorf("provide authn directory table names connector: %w", err)
	}
	if err := module.Provide[OAuthTableNamesConnector](services, OAuthTableNamesConnector{
		Bind: m.foreign.bindOAuth,
	}); err != nil {
		return fmt.Errorf("provide authn oauth table names connector: %w", err)
	}
	if err := module.Provide[identity.PrincipalTableNamesReceiver](services, identity.PrincipalTableNamesReceiver{
		Bind: m.foreign.bindPrincipal,
	}); err != nil {
		return fmt.Errorf("provide authn principal table names receiver: %w", err)
	}
	if err := module.Provide[identity.PrincipalStateReceiver](services, identity.PrincipalStateReceiver{
		Bind: m.bindPrincipalState,
	}); err != nil {
		return fmt.Errorf("provide authn principal state receiver: %w", err)
	}
	if err := module.Provide[identity.PrincipalCredentialStore](services, m.principalCredentialStore()); err != nil {
		return fmt.Errorf("provide authn principal credential store: %w", err)
	}
	if err := module.Provide[CredentialTableNames](services, PublishedCredentialTableNames(m.runtime.TablePrefix())); err != nil {
		return fmt.Errorf("provide authn credential table names: %w", err)
	}
	return nil
}

// bindPrincipalState records identity's principal-state capability. identity
// pushes it during its own injection, which is after authn's, so the module
// stores it and hands it to the repositories it builds in Initialize.
func (m *Module) bindPrincipalState(store identity.PrincipalStateStore) error {
	if m == nil {
		return authnext.ErrUnavailable
	}
	m.principals = store
	return nil
}

// bootstrapCredentialStore is provided before injection, so each closure builds
// its repository at call time from collaborators Wire has since supplied. The
// platform bootstrap runs before Initialize, so it cannot go through Service.
func (m *Module) bootstrapCredentialStore() BootstrapCredentialStore {
	repository := func() (*Repository, error) {
		if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
			return nil, authnext.ErrUnavailable
		}
		repo := NewRepository(m.runtime, m.ids, m.appender)
		// The bootstrap runs after Wire, so the owners have published and the
		// module's holder already carries their names.
		repo.foreign = &m.foreign
		repo.principals = m.principals
		return repo, nil
	}
	return BootstrapCredentialStore{
		Exists: func(ctx context.Context, db *gorm.DB, principalID guid.ID) (bool, error) {
			repo, err := repository()
			if err != nil {
				return false, err
			}
			return repo.HasCredentialOn(ctx, db, principalID)
		},
		Create: func(ctx context.Context, db *gorm.DB, principalID guid.ID, password string, now time.Time) error {
			repo, err := repository()
			if err != nil {
				return err
			}
			return repo.CreateBootstrapCredentialOn(ctx, db, principalID, password, now)
		},
		Reset: func(ctx context.Context, db *gorm.DB, principalID guid.ID, password string, now time.Time) (bool, error) {
			repo, err := repository()
			if err != nil {
				return false, err
			}
			return repo.ResetBootstrapCredentialOn(ctx, db, principalID, password, now)
		},
	}
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, registrarErr := module.Resolve[*authz.Registrar](services)
	if registrarErr != nil && !errors.Is(registrarErr, module.ErrServiceMissing) {
		return registrarErr
	}
	if registrar != nil {
		m.personalRegistry = registrar.Registry()
	}
	grants, grantErr := module.Resolve[OAuthGrantValidator](services)
	if grantErr != nil && !errors.Is(grantErr, module.ErrServiceMissing) {
		return grantErr
	}
	m.oauthGrants = grants
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve authn id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve authn audit appender: %w", err)
	}
	principalCreator, err := module.Resolve[identity.RegistrationPrincipalCreator](services)
	if err != nil {
		return fmt.Errorf("resolve authn registration principal creator: %w", err)
	}
	memberTenant, err := module.Resolve[core.MemberTenantResolver](services)
	if err != nil {
		return fmt.Errorf("resolve authn member tenant resolver: %w", err)
	}
	m.memberTenant = memberTenant
	fields, err := module.Resolve[core.AuthorizationEvaluator](services)
	if err != nil {
		return fmt.Errorf("resolve authn field evaluator: %w", err)
	}
	m.fields = fields
	m.personalPermissions = fields.CheckBulk
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve authn access tables: %w", err)
	}
	if err := m.foreign.bindAccess(accessTables); err != nil {
		return fmt.Errorf("bind authn access tables: %w", err)
	}
	m.ids = ids
	m.appender = appender
	m.registrationCreator = func(
		ctx context.Context,
		db *gorm.DB,
		email, passwordHash, displayName string,
		now time.Time,
		_ guid.Source,
	) (guid.ID, error) {
		id, err := principalCreator(ctx, db, email, passwordHash, displayName, now)
		switch {
		case errors.Is(err, identity.ErrLoginConflict):
			return 0, authnext.ErrRegistrationConflict
		case errors.Is(err, identity.ErrInvalid):
			return 0, authnext.ErrInvalidRegistration
		default:
			return id, err
		}
	}
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := authni18n.Register(); err != nil {
		return fmt.Errorf("register authn i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
		return fmt.Errorf("initialize authn: runtime, id generator, and audit appender are required")
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return fmt.Errorf("initialize authn: the core, identity, organization, and oauth modules must publish the table names authn joins")
	}
	repository := NewRepository(m.runtime, m.ids, m.appender)
	// Wire has already run, so the owners have published; the service and every
	// repository it clones share the one holder the connectors bound.
	repository.foreign = &m.foreign
	repository.principals = m.principals
	service, err := NewWebService(
		m.config.Service,
		repository,
		WithRegistrationPrincipalCreator(m.registrationCreator),
		WithMemberTenantResolver(m.memberTenant),
		WithFieldPermissionChecker(m.fields),
		WithAccountViewAuthorizer(m.accountViewAuthorizer),
	)
	if err != nil {
		return fmt.Errorf("initialize authn service: %w", err)
	}
	m.Service = service
	m.Service.recoveryEligibility = m.recoveryEligibility
	m.Service.federatedProofTrust = m.federatedProofTrust
	m.Service.oauthGrants = m.oauthGrants
	m.Service.personalPermissions = m.personalPermissions
	m.Service.personalRegistry = m.personalRegistry
	return nil
}

// Authenticate is explicit so the module can be published as a verifier
// during Wire without dereferencing its not-yet-initialized embedded service.
func (m *Module) Authenticate(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if m == nil || m.Service == nil {
		return nil, authnext.ErrUnavailable
	}
	return m.Service.Authenticate(ctx, authorization, cookie)
}

func (m *Module) ProjectProfile(ctx context.Context, claims *authnext.Claims, scope string) (ProfileClaims, error) {
	if m == nil || m.Service == nil {
		return ProfileClaims{}, authnext.ErrUnavailable
	}
	return m.Service.ProjectProfile(ctx, claims, scope)
}

func (m *Module) ValidateCSRF(method, origin, cookie, authorization string) error {
	if m == nil || m.Service == nil {
		return authnext.ErrUnavailable
	}
	return m.Service.ValidateCSRF(method, origin, cookie, authorization)
}

func (m *Module) SessionCookieName() string {
	if m == nil || m.Service == nil {
		return ""
	}
	return m.Service.SessionCookieName()
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.Service == nil || m.repo == nil || m.repo.writer() == nil {
		return authnext.ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("register authn: service is unavailable")
	}
	RegisterHTTP(api, m.Service, m.Service)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return fmt.Errorf("start authn: service is unavailable")
	}
	if err := m.Service.repo.backfillPublicIDs(ctx); err != nil {
		return fmt.Errorf("start authn: assign session and passkey public IDs: %w", err)
	}
	return m.StartNotificationWorker(ctx)
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return nil
	}
	err := m.StopNotificationWorker(ctx)
	if m.sharedSecurity != nil {
		err = errors.Join(err, m.sharedSecurity.client.Close())
	}
	return err
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
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ WebService             = (*Module)(nil)
	_ InvitationCredentials  = (*Module)(nil)
	_ authz.TokenVerifier    = (*Module)(nil)
)
