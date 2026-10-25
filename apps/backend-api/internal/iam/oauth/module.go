package oauth

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// ErrUnavailable reports that the OAuth module has not been initialized with
// a writable repository.
var ErrUnavailable = errors.New("OAuth service unavailable")

// Module owns OAuth protocol persistence, client administration, HTTP, and
// locale behavior.
type Config struct {
	module.Config
	// Consent locates the consent screen and bounds how long a parked
	// authorization request stays answerable.
	Consent ConsentConfig
	// Device bounds the device grant: how long a code lasts and how often a
	// device with no browser may ask whether somebody has approved it.
	Device DeviceConfig `mapstructure:"device" group:"device"`
}

type Module struct {
	*Service
	config     Config
	runtime    *appenv.Runtime
	registrar  *authz.Registrar
	authn      authn.WebService
	principals identity.PrincipalStateStore
	ids        guid.Source
	appender   audit.Appender
	// foreign is the one holder of the table names the modules that own them
	// published to oauth. The repository reads through it, so a name that was
	// never published fails the statement instead of reaching the database.
	foreign foreignTables
}

// NewModule builds the OAuth module. Shared business and infrastructure
// services are resolved during module wiring.
func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("OAuth module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string {
	return []string{
		platformi18n.Name, guid.Name, audit.Name,
		core.Name, identity.Name, authn.Name, organization.Name,
	}
}

// ProvideServices exposes grant validation without making authn depend on OAuth.
func (m *Module) ProvideServices(services *module.Services) error {
	if err := m.provideEnrollment(services); err != nil {
		return err
	}
	if err := module.Provide[identity.PrincipalOAuthRevoker](services, func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		if m.Service == nil {
			return ErrUnavailable
		}
		return m.Service.revokePrincipalGrantsOn(ctx, tx, id, 0)
	}); err != nil {
		return err
	}
	if err := module.Provide[core.TenantOAuthRevoker](services, func(ctx context.Context, tx *gorm.DB, tenantID, principalID guid.ID) error {
		if m.Service == nil {
			return ErrUnavailable
		}
		return m.Service.revokeTenantGrantsOn(ctx, tx, tenantID, principalID)
	}); err != nil {
		return err
	}
	return module.Provide[authn.OAuthGrantValidator](services, authn.OAuthGrantValidator(func(ctx context.Context, claims *authnext.Claims) error {
		if m.Service == nil {
			return authnext.ErrUnavailable
		}
		return m.Service.ValidateAccessGrant(ctx, claims)
	}))
}

func (m *Module) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth authorization registrar: %w", err)
	}
	authenticator, err := module.Resolve[authn.WebService](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth authentication service: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth audit appender: %w", err)
	}
	principals, err := module.Resolve[identity.PrincipalStateStore](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth principal state store: %w", err)
	}
	// Each consumer declares the fields it reads and oauth fills exactly those,
	// all from the module's own published struct, so a consumer cannot be handed
	// a name oauth builds a second way.
	published := PublishedGrantTableNames(m.runtime.TablePrefix())
	authnTables, err := module.Resolve[authn.OAuthTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve authn oauth table names connector: %w", err)
	}
	if authnTables.Bind == nil {
		return errors.New("resolve authn oauth table names connector: bind is required")
	}
	if err := authnTables.Bind(authn.OAuthTableNames{
		Clients: published.Clients, RefreshTokens: published.RefreshTokens,
	}); err != nil {
		return fmt.Errorf("bind authn oauth table names: %w", err)
	}
	identityTables, err := module.Resolve[identity.OAuthTableNamesConnector](services)
	if err != nil {
		return fmt.Errorf("resolve identity oauth table names connector: %w", err)
	}
	if identityTables.Bind == nil {
		return errors.New("resolve identity oauth table names connector: bind is required")
	}
	if err := identityTables.Bind(identity.OAuthTableNames{RefreshTokens: published.RefreshTokens}); err != nil {
		return fmt.Errorf("bind identity oauth table names: %w", err)
	}
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth access tables: %w", err)
	}
	principalTables, err := module.Resolve[identity.PrincipalTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth principal tables: %w", err)
	}
	credentialTables, err := module.Resolve[authn.CredentialTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth credential tables: %w", err)
	}
	directoryTables, err := module.Resolve[organization.DirectoryTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve OAuth directory tables: %w", err)
	}
	if err := m.foreign.bind(
		accessTables, principalTables, credentialTables, directoryTables,
	); err != nil {
		return fmt.Errorf("bind OAuth foreign tables: %w", err)
	}
	m.registrar = registrar
	m.authn = authenticator
	m.ids = ids
	m.appender = appender
	m.principals = principals
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if err := oauthi18n.Register(); err != nil {
		return fmt.Errorf("register OAuth i18n: %w", err)
	}
	if m == nil || m.runtime == nil || m.registrar == nil || m.authn == nil || m.ids == nil || m.appender == nil || m.principals.Status == nil {
		return errors.New("initialize OAuth: runtime, authz registrar, authentication, id generator, audit appender, and principal state store are required")
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return errors.New("initialize OAuth: the core, identity, authn, and organization modules must publish the table names OAuth joins")
	}
	m.Service = NewService(m.runtime, m.authn, m.ids, m.appender, m.principals, m.config.Consent, m.config.Device)
	m.Service.repo.foreign = &m.foreign
	// Parse the consent screen now: a misconfigured template file should stop
	// the process here rather than surface as a 500 on someone's first login.
	if _, err := m.Service.consentTemplate(); err != nil {
		return fmt.Errorf("initialize OAuth consent screen: %w", err)
	}
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
		return errors.New("register OAuth: service is unavailable")
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
)
