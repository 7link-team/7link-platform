package oauth

import (
	"context"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	assert.Equal(t, []string{
		platformi18n.Name, guid.Name, audit.Name,
		core.Name, identity.Name, authn.Name, organization.Name,
	}, instance.ModuleDependencies())
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
		&oauthDependencyProvider{name: core.Name, provide: func(services *module.Services) error {
			return module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(harness.prefix))
		}},
		&oauthDependencyProvider{name: authn.Name, provide: func(services *module.Services) error {
			if err := module.Provide[*authz.Registrar](services, oauthTestRegistrar(harness.web)); err != nil {
				return err
			}
			if err := module.Provide[authn.WebService](services, harness.web); err != nil {
				return err
			}
			// The consumers publish these during Wire in a running deployment;
			// this registry fakes them so oauth's own wiring resolves.
			if err := module.Provide[authn.OAuthTableSetConnector](services, authn.OAuthTableSetConnector{
				Bind: func(gormx.TableSet) error { return nil },
			}); err != nil {
				return err
			}
			return module.Provide[authn.CredentialTableNames](services,
				authn.PublishedCredentialTableNames(harness.prefix))
		}},
		&oauthDependencyProvider{
			name:    identity.Name,
			provide: identity.NewModule(harness.runtime, identity.Config{}).ProvideServices,
		},
		&oauthDependencyProvider{name: organization.Name, provide: func(services *module.Services) error {
			return module.Provide[organization.DirectoryTableNames](services,
				organization.PublishedDirectoryTableNames(harness.prefix))
		}},
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

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, oauthTestRegistrar(newOAuthWebFixture())))
	require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
	require.NoError(t, module.Provide[guid.Source](services, newOAuthTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, audit.Appender(
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "principal state store")

	// A consumer that provides a connector without a bind would leave the tables
	// unpublished while module wiring reported success, so wiring refuses it.
	// Both consumers are checked, because a single unchecked one silently
	// reintroduces the empty table name the connectors exist to remove.
	for name, unbound := range map[string]func(*module.Services) error{
		"authn oauth table set connector": func(services *module.Services) error {
			return module.Provide[authn.OAuthTableSetConnector](services, authn.OAuthTableSetConnector{})
		},
		"identity oauth table set connector": func(services *module.Services) error {
			return module.Provide[identity.OAuthTableSetConnector](services, identity.OAuthTableSetConnector{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			services := module.NewServices()
			require.NoError(t, module.Provide[*authz.Registrar](services, oauthTestRegistrar(newOAuthWebFixture())))
			require.NoError(t, module.Provide[authn.WebService](services, newOAuthWebFixture()))
			require.NoError(t, module.Provide[guid.Source](services, newOAuthTestIDs(1)))
			require.NoError(t, module.Provide[audit.Appender](services, audit.Appender(
				func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
			)))
			require.NoError(t, module.Provide[identity.PrincipalStateStore](services, identity.PrincipalStateStore{}))
			require.NoError(t, unbound(services))
			// The other connector is bound, so the refusal can only be the one
			// under test rather than the first one that happens to be missing.
			if name == "identity oauth table set connector" {
				require.NoError(t, module.Provide[authn.OAuthTableSetConnector](services, authn.OAuthTableSetConnector{
					Bind: func(gormx.TableSet) error { return nil },
				}))
			}
			require.NoError(t, services.Seal())
			assert.ErrorContains(t, instance.InjectServices(services), name+": bind is required")
		})
	}
}

// TestOAuthModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. OAuth holds its foreign names as typed
// fields now, so a name that never arrived can no longer be reported by the
// statement that needed it: it is caught once, here, and the deployment does
// not start rather than joining an empty table name.
func TestOAuthModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	access, principal, credential, directory := publishedForeignTables(harness.prefix)

	for _, missing := range []struct {
		owner      string
		access     core.AccessTableNames
		principal  identity.PrincipalTableNames
		credential authn.CredentialTableNames
		directory  organization.DirectoryTableNames
	}{
		{"core", core.AccessTableNames{}, principal, credential, directory},
		{"identity", access, identity.PrincipalTableNames{}, credential, directory},
		{"authn", access, principal, authn.CredentialTableNames{}, directory},
		{"organization", access, principal, credential, organization.DirectoryTableNames{}},
	} {
		t.Run(missing.owner, func(t *testing.T) {
			require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
			instance := NewModule(harness.runtime, enabledModuleConfig())
			services := module.NewServices()
			require.NoError(t, module.Provide[*authz.Registrar](services, oauthTestRegistrar(harness.web)))
			require.NoError(t, module.Provide[authn.WebService](services, harness.web))
			require.NoError(t, module.Provide[guid.Source](services, newOAuthTestIDs(7200)))
			require.NoError(t, module.Provide[audit.Appender](services, harness.repo.appender))
			require.NoError(t, module.Provide[identity.PrincipalStateStore](services,
				resolveIdentityPrincipalState(t, harness.runtime)))
			require.NoError(t, module.Provide[authn.OAuthTableSetConnector](services, authn.OAuthTableSetConnector{
				Bind: func(gormx.TableSet) error { return nil },
			}))
			require.NoError(t, module.Provide[identity.OAuthTableSetConnector](services, identity.OAuthTableSetConnector{
				Bind: func(gormx.TableSet) error { return nil },
			}))
			require.NoError(t, module.Provide[core.AccessTableNames](services, missing.access))
			require.NoError(t, module.Provide[identity.PrincipalTableNames](services, missing.principal))
			require.NoError(t, module.Provide[authn.CredentialTableNames](services, missing.credential))
			require.NoError(t, module.Provide[organization.DirectoryTableNames](services, missing.directory))
			require.NoError(t, services.Seal())

			require.NoError(t, instance.InjectServices(services))
			assert.ErrorContains(t, instance.Initialize(t.Context()),
				"the core, identity, authn, and organization modules must publish the table names OAuth joins")
		})
	}
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
	require.NoError(t, instance.foreign.bind(publishedForeignTables(harness.prefix)))
	assert.ErrorContains(t, instance.Initialize(t.Context()), "initialize OAuth consent screen")
}
