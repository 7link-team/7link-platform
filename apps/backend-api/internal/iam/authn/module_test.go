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
// depends on, then activates and wires the registry. The modules that reach
// authn's connectors are deliberately not among the fakes: a caller that wants
// their names publishes them itself, and a caller that wants to see the module
// refuse to start does not. Initialization is left to the caller, because it is
// the phase that turns the module into a live service.
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
			if err := module.Provide[core.MemberTenantResolver](services, boundMemberTenantResolver(t, harness.runtime)); err != nil {
				return err
			}
			return module.Provide[core.AccessTableNames](services,
				core.PublishedAccessTableNames(harness.runtime.TablePrefix()))
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

// TestAuthnModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. authn holds its foreign names as typed
// fields now, so a name that never arrived can no longer be reported by the
// statement that needed it: it is caught once, here, and the deployment does
// not start rather than joining an empty table name.
func TestAuthnModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	const prefix = "sl_"
	directory := func(t *testing.T, instance *Module) {
		require.NoError(t, instance.foreign.bindDirectory(publishedDirectoryTables(prefix)))
	}
	oauth := func(t *testing.T, instance *Module) {
		require.NoError(t, instance.foreign.bindOAuth(publishedOAuthTables(prefix)))
	}
	principal := func(t *testing.T, instance *Module) {
		require.NoError(t, instance.foreign.bindPrincipal(identity.PublishedPrincipalTableNames(prefix)))
	}

	for _, missing := range []struct {
		owner string
		// access is what core provides during injection; the other three
		// owners reach the module through the connectors it publishes, so an
		// owner that published nothing simply never calls its own.
		access  core.AccessTableNames
		publish []func(*testing.T, *Module)
	}{
		{owner: "core", publish: []func(*testing.T, *Module){directory, oauth, principal}},
		{
			owner:   "identity",
			access:  core.PublishedAccessTableNames(prefix),
			publish: []func(*testing.T, *Module){directory, oauth},
		},
		{
			owner:   "organization",
			access:  core.PublishedAccessTableNames(prefix),
			publish: []func(*testing.T, *Module){oauth, principal},
		},
		{
			owner:   "oauth",
			access:  core.PublishedAccessTableNames(prefix),
			publish: []func(*testing.T, *Module){directory, principal},
		},
		{owner: "every owner"},
	} {
		t.Run(missing.owner, func(t *testing.T) {
			// The startup check runs after the module registers its locales,
			// which the registry initializes ahead of authn in a deployment.
			require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
			instance := NewModule(harness.runtime, authnModuleConfig(localAuthnConfig()))

			services := module.NewServices()
			require.NoError(t, module.Provide[guid.Source](services, newRepositoryTestIDs(5000)))
			require.NoError(t, module.Provide[audit.Appender](services, harness.repository.audit))
			require.NoError(t, module.Provide[identity.RegistrationPrincipalCreator](services,
				identity.RegistrationPrincipalCreator(func(
					context.Context, *gorm.DB, string, string, string, time.Time,
				) (guid.ID, error) {
					return 0, nil
				})))
			require.NoError(t, module.Provide[core.MemberTenantResolver](services,
				boundMemberTenantResolver(t, harness.runtime)))
			require.NoError(t, module.Provide[core.AuthorizationEvaluator](services,
				boundAuthorizationEvaluator(t, harness.runtime)))
			require.NoError(t, module.Provide[core.AccessTableNames](services, missing.access))
			require.NoError(t, services.Seal())
			require.NoError(t, instance.InjectServices(services))

			for _, publish := range missing.publish {
				publish(t, instance)
			}
			require.ErrorContains(t, instance.Initialize(t.Context()),
				"the core, identity, organization, and oauth modules must publish the table names authn joins")
			assert.Nil(t, instance.Service, "a module that refused to start has no service")
		})
	}
}

// TestAuthnReadersUseTheNamesTheOwnersPublished is the other half of that
// guarantee, proven on the module a deployment runs: once every owner has
// published, the readers that need a table authn does not own reach it through
// the one holder the connectors bound. The harness database has the prefixed
// tables, so a name that was not the owner's would come back as a SQL error
// rather than as the reader's own answer.
func TestAuthnReadersUseTheNamesTheOwnersPublished(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	registry, instance := authnWiredRegistry(t, harness, localAuthnConfig())
	// The owners reach the module's connectors during Wire in a running
	// deployment; none of them is among the fakes, so the harness publishes.
	bindModuleForeignTables(t, instance, "sl_")
	require.NoError(t, registry.Initialize(t.Context()))
	ctx := t.Context()

	_, err := instance.Service.repo.oauthClientState(ctx, 1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "a bound reader reaches the table and reports its own answer")
	_, err = instance.Service.repo.serviceAccountState(ctx, 1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	revoked, err := instance.Service.repo.revokeRefreshTokens(ctx, 1, repositoryTestNow)
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
	// The owners reach the module's connectors during Wire in a running
	// deployment; none of them is among the fakes, so the harness publishes
	// before the startup check reads the holder.
	bindModuleForeignTables(t, instance, harness.runtime.TablePrefix())
	require.NoError(t, registry.Initialize(t.Context()))
	require.NotNil(t, instance.Service.oauthGrants)
	assert.ErrorIs(t, instance.Service.oauthGrants(t.Context(), &authnext.Claims{}), assert.AnError)
	assert.Equal(t, config.Web.CookieName, instance.SessionCookieName())
	require.NoError(t, instance.CheckHealth(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	assert.NotNil(t, instance.notificationCancel)

	_, api := humatest.New(t)
	require.NoError(t, registry.RegisterHTTP(t.Context(), api))
	assert.Len(t, authnOperationManifest(api.OpenAPI()), 39)
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

// TestModuleHoldsThePrincipalStateIdentityPushes covers the receiver authn
// provides for identity's capability. authn is activated before identity, so it
// cannot resolve the capability itself; identity pushes it during its own
// injection and the module hands it to the repositories it builds afterwards.
func TestModuleHoldsThePrincipalStateIdentityPushes(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	instance := NewModule(harness.runtime, authnModuleConfig(localAuthnConfig()))

	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	receiver, err := module.Resolve[identity.PrincipalStateReceiver](services)
	require.NoError(t, err)
	require.NotNil(t, receiver.Bind)

	// Before the push there is nothing to call, which is what an unwired module
	// looks like; after it the module carries the owner's statements.
	assert.Nil(t, instance.principals.LockActive)
	published := identity.NewModule(harness.runtime, identity.Config{})
	store, err := identityPrincipalState(t, published)
	require.NoError(t, err)
	require.NoError(t, receiver.Bind(store))
	require.NotNil(t, instance.principals.LockActive)
	assert.NoError(t, instance.principals.LockActive(t.Context(), harness.db, 1))

	var absent *Module
	assert.ErrorIs(t, absent.bindPrincipalState(store), authnext.ErrUnavailable)
}

// identityPrincipalState resolves the capability from identity itself, so this
// test exercises the owner's real statements rather than a stand-in.
func identityPrincipalState(t *testing.T, instance *identity.Module) (identity.PrincipalStateStore, error) {
	t.Helper()
	services := module.NewServices()
	if err := instance.ProvideServices(services); err != nil {
		return identity.PrincipalStateStore{}, err
	}
	if err := services.Seal(); err != nil {
		return identity.PrincipalStateStore{}, err
	}
	return module.Resolve[identity.PrincipalStateStore](services)
}
