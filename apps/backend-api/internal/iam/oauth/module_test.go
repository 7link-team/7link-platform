package oauth

import (
	"context"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
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
)

func enabledModuleConfig() Config {
	return Config{Config: module.Config{Enabled: true}}
}

// resolveIdentityPrincipalState resolves the capability the way module wiring
// does, so a hand-built Module carries the same store production provides.
func resolveIdentityPrincipalState(t *testing.T, runtime *appenv.Runtime) identity.PrincipalStateStore {
	t.Helper()
	services := module.NewServices()
	require.NoError(t, identity.NewModule(runtime, identity.Config{}).ProvideServices(services))
	require.NoError(t, services.Seal())
	store, err := module.Resolve[identity.PrincipalStateStore](services)
	require.NoError(t, err)
	return store
}

func TestModuleActivationComesFromConfig(t *testing.T) {
	assert.False(t, (&Module{}).Enabled())
	assert.True(t, (&Module{config: enabledModuleConfig()}).Enabled())
}

type oauthPermissionChecker struct{}

func (oauthPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (oauthPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type oauthMembershipChecker struct{}

func (oauthMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

type oauthDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (provider *oauthDependencyProvider) ModuleName() string { return provider.name }

func (provider *oauthDependencyProvider) ProvideServices(services *module.Services) error {
	return provider.provide(services)
}

func oauthTestRegistrar(web authz.TokenVerifier) *authz.Registrar {
	return authz.NewRegistrar(authz.DefaultRegistry(), web, oauthPermissionChecker{}, oauthMembershipChecker{})
}

func TestOAuthModuleRequiresRuntimeAtConstruction(t *testing.T) {
	assert.Panics(t, func() { NewModule(nil, Config{}) })
}

func TestOAuthGrantCapabilityFollowsModuleReadiness(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "Capability")
	tokens := grantTokens(t, h, client, secret)
	claims, err := authenticator.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
	require.NoError(t, err)
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	validator, err := module.Resolve[authn.OAuthGrantValidator](services)
	require.NoError(t, err)
	assert.ErrorIs(t, validator(t.Context(), claims), authnext.ErrUnavailable)
	instance.Service = h.service
	require.NoError(t, validator(t.Context(), claims))
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	require.NoError(t, h.service.RevokeConsent(t.Context(), 101, clientID))
	assert.ErrorIs(t, validator(t.Context(), claims), authnext.ErrInvalidToken)
}

func TestOAuthModuleDependenciesAndReadinessBeforeInitialize(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(appenv.NewRuntime(appenv.NewIdentity("oauth-test", "1")), enabledModuleConfig())
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, identity.Name, authn.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "service is unavailable")
	assert.ErrorContains(t, instance.Initialize(t.Context()), "authentication")
}

func TestOAuthModuleResolvesDependenciesAndRegistersContract(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	instance := NewModule(harness.runtime, enabledModuleConfig())
	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&oauthDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newOAuthTestIDs(7000))
		}},
		&oauthDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			return module.Provide[audit.Appender](services, harness.repo.appender)
		}},
		&oauthDependencyProvider{name: authn.Name, provide: func(services *module.Services) error {
			if err := module.Provide[*authz.Registrar](services, oauthTestRegistrar(harness.web)); err != nil {
				return err
			}
			return module.Provide[authn.WebService](services, harness.web)
		}},
		&oauthDependencyProvider{
			name:    identity.Name,
			provide: identity.NewModule(harness.runtime, identity.Config{}).ProvideServices,
		},
		instance,
	))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, oauthOperationManifest(api.OpenAPI()), 17)
}

func TestOAuthModuleReportsMissingWiring(t *testing.T) {
	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("oauth-test", "1")),
		enabledModuleConfig(),
	)
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "authorization registrar")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, oauthTestRegistrar(newOAuthWebFixture())))
	require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, oauthTestRegistrar(newOAuthWebFixture())))
	require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
	require.NoError(t, module.Provide[guid.Source](services, newOAuthTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "audit appender")
}

// TestOAuthModuleRefusesToStartWithAnUnusableConsentScreen keeps a bad template
// path a startup failure. Discovering it lazily would turn a deployment typo
// into a 500 on the first person who tries to log in.
func TestOAuthModuleRefusesToStartWithAnUnusableConsentScreen(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(harness.runtime, Config{
		Config:  module.Config{Enabled: true},
		Consent: ConsentConfig{TemplateFile: filepath.Join(t.TempDir(), "absent.html")},
	})
	instance.registrar = oauthTestRegistrar(harness.web)
	instance.authn = harness.web
	instance.ids = newOAuthTestIDs(7100)
	instance.appender = harness.repo.appender
	instance.principals = resolveIdentityPrincipalState(t, harness.runtime)
	assert.ErrorContains(t, instance.Initialize(t.Context()), "initialize OAuth consent screen")
}
