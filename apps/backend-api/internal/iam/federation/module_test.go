package federation

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	federationi18n "7link.com/platform/internal/iam/federation/i18n"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/extension/keyringx"
	platformi18n "7link.com/platform/pkg/i18n"
	platformmodule "7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testID(value string) guid.ID     { return federationID(value) }
func wireID(value string) string      { return testID(value).String() }
func parseGUID(value string) guid.ID  { id, _ := guid.Parse(value); return id }
func guidString(value guid.ID) string { return value.String() }

func RegisterI18n() error { return federationi18n.Register() }
func RegisterAdminREST(api huma.API, service *Service, registrar *authz.Registrar) {
	registerAdminHTTP(api, service, registrar)
}
func RegisterSAMLAdminREST(api huma.API, service *Service, registrar *authz.Registrar) {
	registerSAMLAdminHTTP(api, service, registrar)
}
func RegisterBrowserREST(api huma.API, service *Service) { registerBrowserHTTP(api, service) }
func RegisterSAMLREST(api huma.API, service *Service, registrar *authz.Registrar) {
	registerSAMLAdminHTTP(api, service, registrar)
	registerSAMLBrowserHTTP(api, service)
}

func TestFederationRuntimeModuleExactOperationManifest(t *testing.T) {
	declaration := federationRuntimeModule(t)
	assert.Equal(t, Name, declaration.ModuleName())
	assert.Equal(t, []string{
		platformi18n.Name, guid.Name, audit.Name,
		core.Name, identity.Name, authn.Name, organization.Name,
	}, declaration.ModuleDependencies())
	require.NoError(t, declaration.Start(t.Context()))
	require.NoError(t, declaration.CheckHealth(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, declaration.RegisterHTTP(t.Context(), api))
	require.NoError(t, authz.ValidateOperations(api, declaration.registrar.Registry()))
	assert.Equal(t, approvedFederationOperations(), federationOperationManifest(api.OpenAPI()))
	assert.Equal(t, "goose_federation", declaration.MigrationSource().TableName)
	assert.Len(t, declaration.MigrationMilestones(), 5)
}

func TestFederationModuleActivationComesFromItsProtocolConfig(t *testing.T) {
	assert.False(t, (&Module{}).Enabled())
	assert.True(t, (&Module{config: Config{Config: platformmodule.Config{Enabled: true}}}).Enabled())
	assert.True(t, (&Module{config: Config{SAML: SAMLConfig{Enabled: true}}}).Enabled())
}

func TestFederationSAMLBrowserOpenAPIContract(t *testing.T) {
	respx.Install()
	declaration := federationRuntimeModule(t)
	_, api := humatest.New(t)
	require.NoError(t, declaration.RegisterHTTP(t.Context(), api))
	spec := api.OpenAPI()

	for path, expected := range map[string][]string{
		"/identity-providers/{provider_id}/callback": {"SAMLResponse", "RelayState"},
		samlSSORoute: {"SAMLRequest", "RelayState"},
		samlSLORoute: {"SAMLRequest", "RelayState"},
	} {
		operation := spec.Paths[path].Post
		require.NotNil(t, operation.RequestBody, path)
		assert.True(t, operation.RequestBody.Required, path)
		media := operation.RequestBody.Content["application/x-www-form-urlencoded"]
		require.NotNil(t, media, path)
		schema := media.Schema
		if schema.Ref != "" {
			schema = spec.Components.Schemas.SchemaFromRef(schema.Ref)
		}
		require.NotNil(t, schema, path)
		properties := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			properties = append(properties, name)
		}
		assert.ElementsMatch(t, expected, properties, path)
		assert.Contains(t, schema.Required, expected[0], path)
	}

	for _, operation := range []*huma.Operation{
		spec.Paths[samlSSORoute].Get, spec.Paths[samlSSORoute].Post,
	} {
		assert.Contains(t, operation.Responses["200"].Content, "text/html")
		assert.Contains(t, operation.Responses, "302")
	}
	for _, operation := range []*huma.Operation{
		spec.Paths[samlSLORoute].Get, spec.Paths[samlSLORoute].Post,
	} {
		assert.Contains(t, operation.Responses["200"].Content, "text/html")
		assert.Contains(t, operation.Responses["200"].Content, "text/plain")
	}
	metadata := spec.Paths[samlMetadataRoute].Get
	assert.Contains(t, metadata.Responses["200"].Content, "application/samlmetadata+xml")
	for _, operation := range []*huma.Operation{
		spec.Paths[samlSSORoute].Get, spec.Paths[samlSSORoute].Post,
		spec.Paths[samlSLORoute].Get, spec.Paths[samlSLORoute].Post, metadata,
	} {
		for _, status := range []string{"400", "422", "500", "503"} {
			assert.Contains(t, operation.Responses, status, operation.OperationID)
		}
	}
}

func TestFederationIdentityContinuationOpenAPIRequiresBoundedStateAndProvider(t *testing.T) {
	declaration := federationRuntimeModule(t)
	_, api := humatest.New(t)
	require.NoError(t, declaration.RegisterHTTP(t.Context(), api))
	operation := api.OpenAPI().Paths["/identity-providers/{provider_id}/identity-continuation"].Get
	parameters := map[string]*huma.Param{}
	for _, parameter := range operation.Parameters {
		parameters[parameter.Name] = parameter
	}
	state, provider := parameters["state"], parameters["provider_id"]
	require.NotNil(t, state)
	require.NotNil(t, provider)
	assert.True(t, state.Required)
	assert.Equal(t, "query", state.In)
	assert.Equal(t, intPointer(1), state.Schema.MinLength)
	assert.Equal(t, intPointer(256), state.Schema.MaxLength)
	assert.True(t, provider.Required)
	assert.Equal(t, "path", provider.In)
	assert.Equal(t, "^[1-9][0-9]*$", provider.Schema.Pattern)
	assert.Equal(t, intPointer(19), provider.Schema.MaxLength)
}

func TestFederationModuleDependencyResolutionAndLifecycle(t *testing.T) {
	env := newFederationEnvironment(t, "")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	config := Config{EncryptionKey: base64.RawStdEncoding.EncodeToString(env.key)}
	instance := NewModule(env.runtime, config)
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "module is unavailable")

	empty := platformmodule.NewServices()
	require.NoError(t, empty.Seal())
	assert.ErrorContains(t, instance.InjectServices(empty), "authorization registrar")

	services := platformmodule.NewServices()
	require.NoError(t, platformmodule.Provide[*authz.Registrar](services, federationRuntimeRegistrar(env.web)))
	require.NoError(t, platformmodule.Provide[guid.Source](services, env.ids))
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	require.NoError(t, platformmodule.Provide[audit.Appender](services, appender))
	require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, env.identity))
	require.NoError(t, platformmodule.Provide[identity.PrincipalStateStore](services, federationPrincipalState(t, env.runtime)))
	require.NoError(t, platformmodule.Provide[authn.WebService](services, env.web))
	provideFederationForeignTables(t, services, env.prefix)
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.Start(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	_, api = humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Equal(t, approvedFederationOperations(), federationOperationManifest(api.OpenAPI()))
}

func TestFederationModuleRejectsMissingDependenciesAndInvalidState(t *testing.T) {
	env := newFederationEnvironment(t, "")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	registrar := federationRuntimeRegistrar(env.web)
	assert.Panics(t, func() { NewModule(nil, Config{}) })

	newInstance := func() *Module { return NewModule(env.runtime, Config{}) }
	servicesWith := func(include int) *platformmodule.Services {
		services := platformmodule.NewServices()
		require.NoError(t, platformmodule.Provide[*authz.Registrar](services, registrar))
		if include > 0 {
			require.NoError(t, platformmodule.Provide[guid.Source](services, env.ids))
		}
		if include > 1 {
			require.NoError(t, platformmodule.Provide[audit.Appender](services, audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })))
		}
		if include > 2 {
			require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, env.identity))
		}
		if include > 3 {
			require.NoError(t, platformmodule.Provide[identity.PrincipalStateStore](services, federationPrincipalState(t, env.runtime)))
		}
		if include > 4 {
			require.NoError(t, platformmodule.Provide[authn.WebService](services, env.web))
		}
		if include > 5 {
			require.NoError(t, platformmodule.Provide[core.AccessTableNames](services,
				core.PublishedAccessTableNames(env.prefix)))
		}
		require.NoError(t, services.Seal())
		return services
	}
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(1)), "audit appender")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(2)), "identity provisioner")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(3)), "principal state store")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(4)), "authentication service")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(5)), "access tables")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(6)), "directory tables")

	var nilModule *Module
	assert.ErrorContains(t, nilModule.Initialize(t.Context()), "module is required")
	_, api := humatest.New(t)
	assert.ErrorContains(t, nilModule.RegisterHTTP(t.Context(), api), "module is unavailable")
	assert.ErrorIs(t, (&Module{}).Start(t.Context()), ErrUnavailable)
	badKey := newInstance()
	badKey.registrar = registrar
	badKey.ids, badKey.appender, badKey.identities, badKey.authn = env.ids, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }, env.identity, env.web
	badKey.principals = federationPrincipalState(t, env.runtime)
	require.NoError(t, badKey.foreign.bind(
		core.PublishedAccessTableNames(env.prefix), organization.PublishedDirectoryTableNames(env.prefix),
	))
	badKey.config.EncryptionKey = "short"
	assert.ErrorContains(t, badKey.Initialize(t.Context()), "encryption key")
}

// TestFederationModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. Federation holds its foreign names as
// typed fields now, so a name that never arrived can no longer be reported by
// the statement that needed it: it is caught once, here, and the deployment
// does not start rather than joining an empty table name.
func TestFederationModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	for _, missing := range []struct {
		owner     string
		access    core.AccessTableNames
		directory organization.DirectoryTableNames
	}{
		{"core", core.AccessTableNames{}, organization.PublishedDirectoryTableNames("")},
		{"organization", core.PublishedAccessTableNames(""), organization.DirectoryTableNames{}},
		{"both", core.AccessTableNames{}, organization.DirectoryTableNames{}},
	} {
		t.Run(missing.owner, func(t *testing.T) {
			env := newFederationEnvironment(t, "")
			require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
			instance := NewModule(env.runtime, Config{EncryptionKey: base64.RawStdEncoding.EncodeToString(env.key)})

			services := platformmodule.NewServices()
			require.NoError(t, platformmodule.Provide[*authz.Registrar](services, federationRuntimeRegistrar(env.web)))
			require.NoError(t, platformmodule.Provide[guid.Source](services, env.ids))
			require.NoError(t, platformmodule.Provide[audit.Appender](services,
				audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })))
			require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, env.identity))
			require.NoError(t, platformmodule.Provide[identity.PrincipalStateStore](services, federationPrincipalState(t, env.runtime)))
			require.NoError(t, platformmodule.Provide[authn.WebService](services, env.web))
			require.NoError(t, platformmodule.Provide[core.AccessTableNames](services, missing.access))
			require.NoError(t, platformmodule.Provide[organization.DirectoryTableNames](services, missing.directory))
			require.NoError(t, services.Seal())

			require.NoError(t, instance.InjectServices(services))
			assert.ErrorContains(t, instance.Initialize(t.Context()),
				"the core and organization modules must publish the table names federation joins")
		})
	}
}

func federationRuntimeModule(t *testing.T) *Module {
	t.Helper()
	env := newFederationEnvironment(t, "openapi_")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(env.runtime, Config{EncryptionKey: base64.RawStdEncoding.EncodeToString(env.key)})
	services := platformmodule.NewServices()
	require.NoError(t, platformmodule.Provide[*authz.Registrar](services, federationRuntimeRegistrar(env.web)))
	require.NoError(t, platformmodule.Provide[guid.Source](services, env.ids))
	require.NoError(t, platformmodule.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, env.identity))
	require.NoError(t, platformmodule.Provide[identity.PrincipalStateStore](services, federationPrincipalState(t, env.runtime)))
	require.NoError(t, platformmodule.Provide[authn.WebService](services, env.web))
	provideFederationForeignTables(t, services, env.prefix)
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, instance.Initialize(t.Context()))
	return instance
}

// provideFederationForeignTables publishes what core and organization publish
// during Wire, so a registry assembled by hand resolves the same sets the
// running deployment does.
func provideFederationForeignTables(t *testing.T, services *platformmodule.Services, prefix string) {
	t.Helper()
	require.NoError(t, platformmodule.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(prefix)))
	require.NoError(t, platformmodule.Provide[organization.DirectoryTableNames](services,
		organization.PublishedDirectoryTableNames(prefix)))
}

func approvedFederationOperations() map[string]string {
	return map[string]string{
		"federation-list-personal-identity-links":           "GET /principals/{principal_id}/identity-links",
		"federation-list-personal-identity-providers":       "GET /principals/{principal_id}/identity-link-providers",
		"federation-create-personal-identity-transaction":   "POST /principals/{principal_id}/identity-link-transactions",
		"federation-delete-personal-identity-link":          "DELETE /principals/{principal_id}/identity-links/{link_id}",
		"federation-complete-personal-identity-transaction": "GET /identity-providers/{provider_id}/identity-continuation",
		"federation-callback-login":                         "GET /identity-providers/{provider_id}/callback", "federation-callback-saml-login": "POST /identity-providers/{provider_id}/callback",
		"federation-create-identity-provider": "POST /tenants/{tenant_id}/identity-providers", "federation-create-saml-service-provider": "POST /tenants/{tenant_id}/saml-service-providers",
		"federation-delete-identity-provider": "DELETE /identity-providers/{provider_id}", "federation-delete-saml-service-provider": "DELETE /saml-service-providers/{sp_id}",
		"federation-list-identity-providers": "GET /tenants/{tenant_id}/identity-providers", "federation-list-saml-service-providers": "GET /tenants/{tenant_id}/saml-service-providers",
		"federation-rotate-saml-signing-key": "POST /tenants/{tenant_id}/saml-signing-keys", "federation-saml-metadata": "GET /saml/{tenant_id}/metadata",
		"federation-saml-slo": "GET /saml/{tenant_id}/slo", "federation-saml-slo-post": "POST /saml/{tenant_id}/slo",
		"federation-saml-sso": "GET /saml/{tenant_id}/sso", "federation-saml-sso-post": "POST /saml/{tenant_id}/sso",
		"federation-start-login": "GET /identity-providers/{provider_id}/authorization", "federation-update-identity-provider": "PUT /identity-providers/{provider_id}",
		"federation-update-saml-service-provider": "PUT /saml-service-providers/{sp_id}",
	}
}

func federationOperationManifest(document *huma.OpenAPI) map[string]string {
	manifest := map[string]string{}
	for path, item := range document.Paths {
		for method, operation := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put, "DELETE": item.Delete} {
			if operation != nil && strings.HasPrefix(operation.OperationID, "federation-") {
				manifest[operation.OperationID] = method + " " + path
			}
		}
	}
	return manifest
}

type federationPermissionChecker struct{}

func (federationPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (federationPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type federationMembershipChecker struct{}

func (federationMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func federationRuntimeRegistrar(verifier authz.TokenVerifier) *authz.Registrar {
	return authz.NewRegistrar(
		authz.DefaultRegistry(), verifier, federationPermissionChecker{}, federationMembershipChecker{},
	)
}

// TestFederationModulePublishesTheSecretRewrapper covers the provider phase,
// which runs before Initialize. The module publishes itself rather than the
// service, because the service does not exist yet; the capability therefore has
// to resolve straight away and answer that federation is unavailable until it
// does, instead of being absent from the registry.
func TestFederationModulePublishesTheSecretRewrapper(t *testing.T) {
	env := newFederationEnvironment(t, "")
	instance := NewModule(env.runtime, Config{})

	services := platformmodule.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	// A second publication of the same capability is a wiring mistake. It is
	// reported as one rather than quietly replacing the first module's entry.
	assert.ErrorContains(t, instance.ProvideServices(services), "provide federation secret rewrapper")

	require.NoError(t, services.Seal())
	rewrapper, err := platformmodule.Resolve[SecretRewrapper](services)
	require.NoError(t, err)
	assert.Same(t, instance, rewrapper)

	_, err = rewrapper.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable, "the capability resolves before Initialize and says so")
}
