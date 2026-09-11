package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type authnDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (m *authnDependencyProvider) ModuleName() string { return m.name }

func (m *authnDependencyProvider) ProvideServices(services *module.Services) error {
	return m.provide(services)
}

func TestAuthnModuleIdentityDependenciesAndReadiness(t *testing.T) {
	assert.Panics(t, func() { NewModule(nil, authnModuleConfig(localAuthnConfig())) })
	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("authn-test", "1")),
		authnModuleConfig(localAuthnConfig()),
	)
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, core.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), authnext.ErrUnavailable)

	_, api := humatest.New(t)
	assert.Error(t, instance.RegisterHTTP(t.Context(), api))
	assert.Error(t, instance.Start(t.Context()))
	assert.NoError(t, instance.Stop(t.Context()))
}

// authnWiredRegistry registers authn with fakes standing in for the modules it
// depends on, then activates and wires the registry. The table-name owners are
// deliberately not among the fakes: a caller that wants their names publishes
// them itself. Initialization is left to the caller, because it is the phase
// that turns the module into a live service.
func authnWiredRegistry(t *testing.T, harness *authnRepositoryHarness, config authnext.Config) (*module.Registry, *Module) {
	t.Helper()

	instance := NewModule(harness.runtime, authnModuleConfig(config))
	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&authnDependencyProvider{name: "oauth-grants-test", provide: func(services *module.Services) error {
			return module.Provide[OAuthGrantValidator](services, OAuthGrantValidator(func(context.Context, *authnext.Claims) error {
				return assert.AnError
			}))
		}},
		&authnDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newRepositoryTestIDs(5000))
		}},
		&authnDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			return module.Provide[audit.Appender](services, harness.repository.audit)
		}},
		&authnDependencyProvider{name: core.Name, provide: func(services *module.Services) error {
			if err := module.Provide[core.AuthorizationEvaluator](services, boundAuthorizationEvaluator(t, harness.runtime)); err != nil {
				return err
			}
			return module.Provide[core.MemberTenantResolver](services, boundMemberTenantResolver(t, harness.runtime))
		}},
		&authnDependencyProvider{name: identity.Name, provide: func(services *module.Services) error {
			creator := registrationCreatorForHarness(harness)
			return module.Provide[identity.RegistrationPrincipalCreator](
				services,
				identity.RegistrationPrincipalCreator(func(
					ctx context.Context,
					db *gorm.DB,
					email, passwordHash, displayName string,
					now time.Time,
				) (guid.ID, error) {
					return creator(ctx, db, email, passwordHash, displayName, now, newRepositoryTestIDs(8000))
				}),
			)
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	return registry, instance
}

// TestAuthnReadersFailClosedUntilTheOwnersPublish is the guarantee the table-set
// connectors exist for, proven on the module a deployment runs: with neither
// owner in wiring, every reader that needs a table authn does not own reports
// the repository unavailable instead of running a statement against an empty
// table name. The harness database has the tables, so an empty name would come
// back as a SQL error rather than as this one.
func TestAuthnReadersFailClosedUntilTheOwnersPublish(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	registry, instance := authnWiredRegistry(t, harness, localAuthnConfig())
	require.NoError(t, registry.Initialize(t.Context()))
	ctx := t.Context()

	_, err := instance.Service.repo.oauthClientState(ctx, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = instance.Service.repo.revokeRefreshTokens(ctx, 1, repositoryTestNow)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = instance.Service.repo.serviceAccountState(ctx, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	var revoked int64
	require.NoError(t, harness.db.Table("sl_iam_refresh_tokens").
		Where("principal_id = ?", 1).Count(&revoked).Error)
	assert.Zero(t, revoked, "a rejected revocation must not touch a row")

	// The owners publish, and the same readers serve the same calls.
	bindModuleForeignTables(t, instance, "sl_")
	_, err = instance.Service.repo.oauthClientState(ctx, 1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "a bound reader reaches the table and reports its own answer")
	revoked, err = instance.Service.repo.revokeRefreshTokens(ctx, 1, repositoryTestNow)
	require.NoError(t, err)
	assert.Zero(t, revoked)
}

func TestAuthnModuleResolvesDependenciesAndRunsLifecycle(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	notifications := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notifications.Close)
	config := localAuthnConfig()
	config.Recovery = authnext.RecoveryConfig{
		Enabled: true, TokenTTL: 15 * time.Minute, Cooldown: time.Hour,
		ResetURL: "https://app.example.test/recover",
	}
	config.Notification = authnext.NotificationConfig{
		URL: notifications.URL, PollInterval: time.Minute, RequestTimeout: time.Second, MaxAttempts: 1,
	}
	registry, instance := authnWiredRegistry(t, harness, config)

	active, exists := registry.ByName(Name)
	require.True(t, exists)
	assert.Same(t, instance, active)

	web, err := module.Resolve[WebService](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, web)
	invitations, err := module.Resolve[InvitationCredentials](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, invitations)
	verifier, err := module.Resolve[authz.TokenVerifier](registry.Services())
	require.NoError(t, err)
	_, err = verifier.Authenticate(t.Context(), "", "")
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
	assert.ErrorIs(t, instance.ValidateCSRF(http.MethodPost, "", "", ""), authnext.ErrUnavailable)
	assert.Empty(t, instance.SessionCookieName())
	require.NoError(t, registry.Initialize(t.Context()))
	require.NotNil(t, instance.Service.oauthGrants)
	assert.ErrorIs(t, instance.Service.oauthGrants(t.Context(), &authnext.Claims{}), assert.AnError)
	assert.Equal(t, config.Web.CookieName, instance.SessionCookieName())
	require.NoError(t, instance.CheckHealth(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	assert.NotNil(t, instance.notificationCancel)

	_, api := humatest.New(t)
	require.NoError(t, registry.RegisterHTTP(t.Context(), api))
	assert.Len(t, authnOperationManifest(api.OpenAPI()), 38)
	require.NoError(t, registry.Stop(context.Background()))
	assert.Nil(t, instance.notificationCancel)
	require.NoError(t, registry.Deinitialize(context.Background()))
}

func TestAuthnModuleReportsMissingWiring(t *testing.T) {
	services := module.NewServices()
	require.NoError(t, services.Seal())
	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("authn-test", "1")),
		authnModuleConfig(localAuthnConfig()),
	)
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authn id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, newRepositoryTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authn audit appender")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, newRepositoryTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authn registration principal creator")

	providers := module.NewServices()
	require.NoError(t, instance.ProvideServices(providers))
	assert.ErrorContains(t, instance.ProvideServices(providers), "PrincipalCredentialsRevoker")
}

func authnModuleConfig(service authnext.Config) Config {
	return Config{Config: module.Config{Enabled: service.Enabled}, Service: service}
}
