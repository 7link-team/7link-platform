package oauth

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
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

func TestOAuthModuleRequiresConstructionCollaborators(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("oauth-test", "1"))
	registrar := oauthTestRegistrar(newOAuthWebFixture())
	assert.Panics(t, func() { NewModule(nil, registrar, Config{}) })
	assert.Panics(t, func() { NewModule(runtime, nil, Config{}) })
	assert.Panics(t, func() { NewModule(runtime, authz.NewAuthenticationOnlyRegistrar(newOAuthWebFixture()), Config{}) })
	assert.Panics(t, func() { NewModule(runtime, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()), Config{}) })
}

func TestOAuthModuleDependenciesAndReadinessBeforeInitialize(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(appenv.NewRuntime(appenv.NewIdentity("oauth-test", "1")), oauthTestRegistrar(newOAuthWebFixture()), enabledModuleConfig())
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, authn.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "service is unavailable")
	assert.ErrorContains(t, instance.Initialize(t.Context()), "authentication")
}

func TestOAuthModuleResolvesDependenciesAndRegistersContract(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	instance := NewModule(harness.runtime, oauthTestRegistrar(harness.web), enabledModuleConfig())
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
			return module.Provide[authn.WebService](services, harness.web)
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, oauthOperationManifest(api.OpenAPI()), 12)
}

func TestOAuthModuleReportsMissingWiring(t *testing.T) {
	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("oauth-test", "1")),
		oauthTestRegistrar(newOAuthWebFixture()),
		enabledModuleConfig(),
	)
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "authentication service")

	services = module.NewServices()
	require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
	require.NoError(t, module.Provide[guid.Source](services, newOAuthTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "audit appender")
}
