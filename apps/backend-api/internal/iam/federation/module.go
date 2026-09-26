package federation

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	federationi18n "7link.com/platform/internal/iam/federation/i18n"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("federation service unavailable")

type Module struct {
	service                 *Service
	runtime                 *appenv.Runtime
	registrar               *authz.Registrar
	config                  Config
	ids                     guid.Source
	appender                audit.Appender
	identities              identity.IdentityProvisioner
	principals              identity.PrincipalStateStore
	authn                   authn.WebService
	accountProofs           authn.ReauthenticationConsumer
	accountReauthentication authn.FederatedReauthentication
	revokeCredentials       identity.PrincipalCredentialsRevoker
	revokeOAuth             identity.PrincipalOAuthRevoker
	// foreign is the one holder of the table names the modules that own them
	// published to federation. The repository reads through it, so a name that
	// was never published fails the statement instead of reaching the database.
	foreign foreignTables
}

func NewModule(runtime *appenv.Runtime, config Config) *Module {
	if runtime == nil {
		panic("federation module requires runtime")
	}
	return &Module{runtime: runtime, config: config}
}

func (m *Module) Enabled() bool {
	return m != nil && (m.config.Enabled || m.config.SAML.Enabled)
}

func (*Module) ModuleDependencies() []string {
	return []string{
		platformi18n.Name, guid.Name, audit.Name,
		core.Name, identity.Name, authn.Name, organization.Name,
	}
}

// ProvideServices publishes the module itself, so the capability resolves the
// service at call time. The service only exists after Initialize, and the
// provider phase runs before that.
func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[SecretRewrapper](services, m); err != nil {
		return fmt.Errorf("provide federation secret rewrapper: %w", err)
	}
	if err := module.Provide[LegacyIdentityMaintenance](services, m); err != nil {
		return fmt.Errorf("provide legacy identity maintenance: %w", err)
	}
	return nil
}

func (m *Module) InjectServices(services *module.Services) error {
	proofs, proofErr := module.Resolve[authn.ReauthenticationConsumer](services)
	if proofErr != nil && !errors.Is(proofErr, module.ErrServiceMissing) {
		return proofErr
	}
	accountReauthentication, accountErr := module.Resolve[authn.FederatedReauthentication](services)
	if accountErr != nil && !errors.Is(accountErr, module.ErrServiceMissing) {
		return accountErr
	}
	m.accountProofs, m.accountReauthentication = proofs, accountReauthentication
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return fmt.Errorf("resolve federation authorization registrar: %w", err)
	}
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return fmt.Errorf("resolve federation id generator: %w", err)
	}
	appender, err := module.Resolve[audit.Appender](services)
	if err != nil {
		return fmt.Errorf("resolve federation audit appender: %w", err)
	}
	identities, err := module.Resolve[identity.IdentityProvisioner](services)
	if err != nil {
		return fmt.Errorf("resolve federation identity provisioner: %w", err)
	}
	principals, err := module.Resolve[identity.PrincipalStateStore](services)
	if err != nil {
		return fmt.Errorf("resolve federation principal state store: %w", err)
	}
	authenticator, err := module.Resolve[authn.WebService](services)
	if err != nil {
		return fmt.Errorf("resolve federation authentication service: %w", err)
	}
	accessTables, err := module.Resolve[core.AccessTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve federation access tables: %w", err)
	}
	directoryTables, err := module.Resolve[organization.DirectoryTableNames](services)
	if err != nil {
		return fmt.Errorf("resolve federation directory tables: %w", err)
	}
	m.revokeCredentials, err = module.Resolve[identity.PrincipalCredentialsRevoker](services)
	if err != nil {
		return fmt.Errorf("resolve federation credential invalidation: %w", err)
	}
	m.revokeOAuth, err = module.Resolve[identity.PrincipalOAuthRevoker](services)
	if err != nil {
		return fmt.Errorf("resolve federation OAuth invalidation: %w", err)
	}
	recovery, err := module.Resolve[authn.RecoveryEligibilityConnector](services)
	if err != nil {
		return err
	}
	if recovery.Bind == nil {
		return ErrUnavailable
	}
	proofTrust, err := module.Resolve[authn.FederatedProofTrustConnector](services)
	if err != nil {
		return err
	}
	if proofTrust.Bind == nil {
		return ErrUnavailable
	}
	if err := proofTrust.Bind(func(ctx context.Context, tx *gorm.DB, id guid.ID, linkID, fingerprint string) error {
		if m.service == nil {
			return ErrUnavailable
		}
		return m.service.proofSourceTrusted(ctx, tx, id, linkID, fingerprint)
	}); err != nil {
		return err
	}
	if err := recovery.Bind(func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		if m.service == nil {
			return ErrUnavailable
		}
		return m.service.recoveryEligible(ctx, tx, id)
	}); err != nil {
		return err
	}
	if err := m.foreign.bind(accessTables, directoryTables); err != nil {
		return fmt.Errorf("bind federation foreign tables: %w", err)
	}
	m.registrar, m.ids, m.appender, m.identities, m.principals, m.authn = registrar, ids, appender, identities, principals, authenticator
	return nil
}

func (m *Module) Initialize(context.Context) error {
	if m == nil {
		return errors.New("initialize federation: module is required")
	}
	if err := federationi18n.Register(); err != nil {
		return fmt.Errorf("register federation i18n: %w", err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil ||
		m.registrar == nil || m.ids == nil || m.appender == nil || m.identities == nil || m.authn == nil ||
		m.principals.Status == nil {
		return errors.New("initialize federation: runtime database, authz registrar, id generator, audit appender, identity provisioner, principal state store, and authentication service are required")
	}
	// Checked here rather than in every statement: injection has run, so an
	// owner that published nothing is a deployment that must not start.
	if !m.foreign.bound() {
		return errors.New("initialize federation: the core and organization modules must publish the table names federation joins")
	}
	keys, err := ResolveKeyring(m.config)
	if err != nil {
		return fmt.Errorf("resolve federation encryption key: %w", err)
	}
	outbound, err := ResolveOutboundPolicy(m.config)
	if err != nil {
		return fmt.Errorf("resolve federation outbound policy: %w", err)
	}
	m.service = NewService(m.runtime, m.appender, m.identities, m.principals, m.authn, m.config, keys, m.ids, outbound)
	m.service.repo.foreign = &m.foreign
	m.service.accountProofs, m.service.accountReauthentication = m.accountProofs, m.accountReauthentication
	m.service.revokeCredentials, m.service.revokeOAuth = m.revokeCredentials, m.revokeOAuth
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.service == nil {
		return ErrUnavailable
	}
	if err := m.service.QuarantineLegacyIdentityLinks(ctx); err != nil {
		return err
	}
	return m.service.StartSAML(ctx, m.config.SAML)
}

func (m *Module) CheckHealth(context.Context) error {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.repo.writer() == nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return errors.New("register federation: module is unavailable")
	}
	if m.service == nil {
		return errors.New("register federation: service is unavailable")
	}
	RegisterHTTP(api, m.service, m.registrar)
	return nil
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Enabler         = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.HTTPRegistrar   = (*Module)(nil)
)
