package app

import (
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newOpenAPIExportConfig owns the minimal application fixture needed to compose
// and migrate the API without depending on an IAM maintenance command.
func newOpenAPIExportConfig(t *testing.T) OpenAPIExportConfig {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	config := validTestAppConfig()
	config.Version = "1"
	config.Migrate = true
	config.Database = map[string]Database{
		"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "openapi.sqlite"), Writable: true, Readable: true},
	}
	config.Files.LocalRoot = filepath.Join(t.TempDir(), "files")
	// Match the composed fixture: permit reads during startup writes and wait
	// for competing subscriber transactions instead of failing on a file lock.
	database := config.Database["primary"]
	database.Dsn += "?_journal_mode=WAL&_busy_timeout=30000"
	config.Database["primary"] = database
	config.Authn = IAMAuthn(authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", SigningKey: key,
		AccessTokenTTL: time.Hour,
		MFA:            authnext.MFAConfig{EncryptionKey: key},
	})
	return OpenAPIExportConfig{AppConfig: config}
}

// TestOpenAPIExportDescribesTheRunningApplicationWithoutRunningIt is the whole
// point of the command: the document that was previously reachable only from a
// listening process, produced by one that never binds an address.
func TestOpenAPIExportDescribesTheRunningApplicationWithoutRunningIt(t *testing.T) {
	document, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)

	var specification struct {
		OpenAPI string                           `json:"openapi"`
		Info    struct{ Title, Version string }  `json:"info"`
		Paths   map[string]map[string]any        `json:"paths"`
		Comps   struct{ Schemas map[string]any } `json:"components"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	assert.Equal(t, "3.1.0", specification.OpenAPI)
	assert.Equal(t, "1", specification.Info.Version, "the document carries the configured version")
	assert.NotEmpty(t, specification.Paths)
	assert.NotEmpty(t, specification.Comps.Schemas,
		"a document with paths and no schemas describes requests nobody can build")
	// A named operation rather than only a count: a document that lost every IAM
	// route would still have paths, and a count would not notice.
	assert.Contains(t, specification.Paths, "/tenants")

	assert.True(t, strings.HasSuffix(string(document), "}\n"),
		"a file people and tools read line by line ends with a newline")
}

// Every group is owned by the final URI prefix, including expanded mounts.
func TestOpenAPIExportGroupsOperationsByPathPrefix(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Authn.Web = authnext.WebConfig{
		Enabled: true, PostLoginURL: "https://iam.example.test/",
		AllowedReturnURLs: []string{"https://iam.example.test/"},
		AllowedOrigins:    []string{"https://iam.example.test"},
	}
	document, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)

	var specification struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))

	for _, path := range []string{
		"/principal-groups/{group_id}",
		"/membership-invitations/acceptances", "/membership-invitations/{invitation_id}/deliveries",
		"/email-verifications/requests", "/email-verifications",
		"/role-access-requests/{request_id}/decision", "/role-access-reviews/{review_id}",
		"/tenants/{tenant_id}/authorization-relationships", "/authorization-scope-model",
	} {
		assert.Contains(t, specification.Paths, path)
	}
	retired := map[string]bool{
		"groups": true, "positions": true, "job-positions": true, "invitations": true, "invitation-acceptances": true,
		"email-verification-requests": true, "access-requests": true, "access-reviews": true,
		"relationships": true, "scope-model": true,
	}
	for path, item := range specification.Paths {
		for _, segment := range strings.Split(path, "/") {
			assert.False(t, retired[segment], "retired resource name: %s", path)
		}
		for method, operation := range item {
			if operation.OperationID == "" {
				continue
			}
			location := method + " " + path
			if !assert.Len(t, operation.Tags, 1, location) {
				continue
			}
			assert.Equal(t, strings.Split(strings.TrimPrefix(path, "/"), "/")[0], operation.Tags[0], location)
		}
	}
}

// TestOpenAPIExportProducesTheSameBytesEveryTime is what makes the document
// usable as a checked-in file. If it varied between runs, every rebuild would
// report a difference, and a real API change would be indistinguishable from
// noise -- which is the failure that makes people stop reading the diff.
func TestOpenAPIExportProducesTheSameBytesEveryTime(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	first, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)
	second, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

// TestOpenAPIExportRefusesToGuessAFormat keeps a misspelled format from quietly
// producing the default. The mistake would otherwise be found by whatever
// consumed the file, a long way from the command that made it.
func TestOpenAPIExportRefusesToGuessAFormat(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Format = "toml"
	_, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.Error(t, err)
	assert.ErrorContains(t, err, "toml")
	assert.ErrorContains(t, err, "json")

	for _, accepted := range []string{"", "json", "JSON", " yaml ", "yml"} {
		format, err := normalizeOpenAPIFormat(accepted)
		require.NoError(t, err, accepted)
		assert.Contains(t, []string{OpenAPIFormatJSON, OpenAPIFormatYAML}, format)
	}
}

// TestOpenAPIExportWritesYAMLWhenAskedTo covers the second serialization. Both
// formats have to describe the same API, or the choice of format would change
// what a generator believes the service accepts.
func TestOpenAPIExportWritesYAMLWhenAskedTo(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Format = OpenAPIFormatYAML
	document, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)

	var fromYAML map[string]any
	require.NoError(t, yaml.Unmarshal(document, &fromYAML))
	assert.Equal(t, "3.1.0", fromYAML["openapi"])

	asJSON, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)
	var fromJSON map[string]any
	require.NoError(t, json.Unmarshal(asJSON, &fromJSON))
	yamlPaths, _ := fromYAML["paths"].(map[string]any)
	jsonPaths, _ := fromJSON["paths"].(map[string]any)
	require.NotEmpty(t, yamlPaths)
	assert.Len(t, yamlPaths, len(jsonPaths), "the format may not change which operations exist")
}

// TestOpenAPIExportKeepsTheVersionStableWhenNoneIsConfigured guards the field
// that would otherwise move on its own. The service stamps its build here, and
// a build stamp in a checked-in document means every commit produces a
// difference that says nothing about the API.
func TestOpenAPIExportKeepsTheVersionStableWhenNoneIsConfigured(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Version = ""
	document, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)

	var specification struct {
		Info struct{ Version string } `json:"info"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	assert.Equal(t, openAPIFallbackVersion, specification.Info.Version)
	assert.NotEqual(t, "dev", specification.Info.Version,
		"the build stamp belongs to the running process, not to a document in a repository")
}

const iamHTTPMappingCount = 369

type iamHTTPMapping struct {
	sourceOperation string
	targetOperation string
	method          string
	path            string
}

type iamHTTPAuthorization struct {
	class      string
	permission string
}

func TestIAMAuthorizationClassifierDistinguishesGuardBoundaries(t *testing.T) {
	_, api := humatest.New(t)
	registrar := authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry())
	handler := func(context.Context, *struct{}) (*struct{ Body string }, error) {
		return &struct{ Body string }{Body: "ok"}, nil
	}
	authz.RegisterPublic(api, huma.Operation{OperationID: "public", Method: http.MethodGet, Path: "/public"}, handler)
	authz.RegisterAuthenticated(registrar, api, huma.Operation{OperationID: "authenticated", Method: http.MethodGet, Path: "/authenticated"}, handler)
	authz.RegisterTenantMember(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tenant-member", Method: http.MethodGet, Path: "/tenant-member"}, handler)
	authz.Register(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tenant", Method: http.MethodGet, Path: "/tenant"}, authz.Guard{Resource: "store", Verb: "view"}, handler)
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "platform", Method: http.MethodGet, Path: "/system"}, authz.Guard{Resource: "tenant", Verb: "view"}, handler)
	authz.RegisterOperatingPlatform(registrar, api, huma.Operation{OperationID: "operating-platform", Method: http.MethodGet, Path: "/roles", Tags: []string{"operating-platforms"}}, authz.Guard{Resource: "role", Verb: "view"}, handler)
	authz.RegisterOperatingPlatformMember(registrar, api, huma.Operation{OperationID: "operating-platform-member", Method: http.MethodGet, Path: "/navigation", Tags: []string{"operating-platforms"}}, handler)
	authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "entity-header", Method: http.MethodGet, Path: "/entity-header"}, authz.Guard{Resource: "store", Verb: "view"}, handler)
	authz.RegisterEntityAdapter(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "entity-query", Method: http.MethodGet, Path: "/entity-query"}, authz.Guard{Resource: "store", Verb: "view"}, func(huma.Context) {})

	expected := map[string]iamHTTPAuthorization{
		"/public":                                       {class: "public"},
		"/authenticated":                                {class: "authenticated"},
		"/tenants/{tenant_id}/tenant-member":            {class: "tenant-member"},
		"/tenants/{tenant_id}/tenant":                   {class: "tenant", permission: "store_view"},
		"/system":                                       {class: "platform", permission: "tenant_view"},
		"/operating-platforms/{platform_id}/roles":      {class: "operating-platform", permission: "role_view"},
		"/operating-platforms/{platform_id}/navigation": {class: "operating-platform-member"},
		"/tenants/{tenant_id}/entities/{entity_id}/entity-header": {class: "entity-path", permission: "store_view"},
		"/tenants/{tenant_id}/entities/{entity_id}/entity-query":  {class: "entity-path", permission: "store_view"},
	}
	for path, want := range expected {
		got, err := iamAuthorizationOf(api.OpenAPI().Paths[path].Get)
		require.NoError(t, err, path)
		assert.Equal(t, want, got, path)
	}

	tenant := api.OpenAPI().Paths["/tenants/{tenant_id}/tenant"].Get
	tenant.Extensions[authz.GuardExtensionKey] = "role_view"
	_, err := iamAuthorizationOf(tenant)
	assert.ErrorContains(t, err, "does not match")

	authenticated := api.OpenAPI().Paths["/authenticated"].Get
	authenticated.Security = nil
	_, err = iamAuthorizationOf(authenticated)
	assert.ErrorContains(t, err, "has no security requirement")

	entity := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/entity-header"].Get
	for _, parameter := range entity.Parameters {
		if parameter.Name == authz.EntityPath && parameter.In == "path" {
			parameter.Required = false
		}
	}
	_, err = iamAuthorizationOf(entity)
	assert.ErrorContains(t, err, "is optional")
}

// bootstrapComposedApp drives the production composition with every IAM and
// business module enabled on a migrated SQLite database. extra modules join the
// inventory after the production modules, before Wire, the way a new business
// module would.
func bootstrapComposedApp(t *testing.T, extra ...module.Module) *App {
	return bootstrapComposedAppConfigured(t, nil, extra...)
}

func bootstrapComposedAppConfigured(t *testing.T, configure func(*AppConfig), extra ...module.Module) *App {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	app := &App{config: AppConfig{
		Name: "iam-openapi-test", Version: "1", Timezone: "UTC", Migrate: true,
		Database: map[string]Database{"primary": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-openapi.sqlite"), Writable: true, Readable: true,
		}},
		Authn: IAMAuthn(authnext.Config{
			Enabled: true, Issuer: "https://iam.example.test", SigningKey: key, AccessTokenTTL: time.Hour,
			Web: authnext.WebConfig{
				Enabled: true, PostLoginURL: "https://app.example.test/home",
				PostLogoutURL:     "https://app.example.test/signed-out",
				AllowedReturnURLs: []string{"https://app.example.test/home"},
				AllowedOrigins:    []string{"https://app.example.test"},
			},
			MFA: authnext.MFAConfig{EncryptionKey: key},
		}),
		Federation: IAMFederation{Enabled: true, EncryptionKey: key},
		Webhook:    IAMWebhook{Enabled: true, EncryptionKey: key},
		Secrets:    testBusinessSecretsConfig(t),
	}}
	// Subscribers start before later modules seed their data. WAL lets readers
	// coexist with startup writes; competing writers need time to finish seeding.
	if configure != nil {
		configure(&app.config)
	}
	database := app.config.Database["primary"]
	database.Dsn += "?_journal_mode=WAL&_busy_timeout=30000"
	app.config.Database["primary"] = database
	require.NoError(t, app.initializeRuntime(t.Context()))
	require.NoError(t, app.composeModules())
	require.NoError(t, app.addModule(extra...))
	require.NoError(t, app.prepareModules())
	require.NoError(t, app.lifecycle(t.Context()))
	t.Cleanup(func() { assert.NoError(t, app.shutdown()) })
	return app
}

// TestComposedRouteGuardsUseOnlySharedRegistryPermissions iterates every
// operation of the composed application, not only the IAM ones: a business
// module's guard naming a code it never contributed would authorize against a
// permission no role can hold.
func TestComposedRouteGuardsUseOnlySharedRegistryPermissions(t *testing.T) {
	app := bootstrapComposedApp(t)
	api := humax.New(chi.NewRouter(), humax.Config{Name: app.config.Name, Version: app.config.Version})
	require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
	registrar, err := module.Resolve[*authz.Registrar](app.modules.Services())
	require.NoError(t, err)
	registry := registrar.Registry()

	guarded := map[string]bool{}
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range openAPIPathOperations(item) {
			if operation == nil {
				continue
			}
			code, isGuarded := authz.OperationGuard(operation)
			if !isGuarded {
				continue
			}
			_, declared := registry.Find(code)
			assert.True(t, declared, "%s %s (%s) is guarded by %q, which the shared registry does not declare", method, path, operation.OperationID, code)
			guarded[code] = true
		}
	}
	// The walk must reach the modules that contribute their own codes, or it
	// proves nothing about the contribution path.
	for _, code := range []string{"todo_view", "todo_import", "file_view", "system_file_view", "role_view"} {
		assert.True(t, guarded[code], "no composed route is guarded by %s", code)
	}
}

func TestComposedIAMOpenAPIHasExactMappedOperationSet(t *testing.T) {
	app := bootstrapComposedApp(t)

	api := humax.New(chi.NewRouter(), humax.Config{Name: app.config.Name, Version: app.config.Version})
	require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
	registrar, err := module.Resolve[*authz.Registrar](app.modules.Services())
	require.NoError(t, err)

	mappings := loadIAMHTTPMappings(t)
	require.Len(t, mappings, iamHTTPMappingCount)
	expected := make(map[string]struct{}, len(mappings))
	sources := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		_, duplicateSource := sources[mapping.sourceOperation]
		assert.False(t, duplicateSource, "duplicate source operation %s", mapping.sourceOperation)
		sources[mapping.sourceOperation] = struct{}{}

		target := mapping.targetOperation + "|" + mapping.method + "|" + mapping.path
		_, duplicateTarget := expected[target]
		assert.False(t, duplicateTarget, "target operation is mapped more than once: %s", target)
		expected[target] = struct{}{}
	}
	require.Len(t, sources, iamHTTPMappingCount)
	require.Len(t, expected, iamHTTPMappingCount)

	actual := make(map[string]struct{}, iamHTTPMappingCount)
	actualAuthorizations := make(map[string]iamHTTPAuthorization, iamHTTPMappingCount)
	operationIDs := make(map[string]string)
	for path, item := range api.OpenAPI().Paths {
		require.NotContains(t, path, "/me/", "personal resources require an explicit principal identifier")
		for method, operation := range openAPIPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			if previous, exists := operationIDs[operation.OperationID]; exists {
				t.Errorf("duplicate OperationID %q at %s and %s", operation.OperationID, previous, location)
			}
			operationIDs[operation.OperationID] = location
			assert.Equal(t, strings.ToLower(operation.OperationID), operation.OperationID, "OperationIDs are lowercase: %s", location)
			// Under /system a tenant_id path parameter would read as tenant-gated
			// admission, so system routes name the root they select owner_id.
			if authz.IsPlatformOperation(operation) && strings.HasPrefix(path, "/system/") {
				for _, parameter := range operation.Parameters {
					assert.False(t, parameter.In == "path" && parameter.Name == "tenant_id", "%s is system-scoped but declares path tenant_id", location)
				}
			}
			if !isIAMOperation(operation.OperationID) {
				continue
			}
			target := operation.OperationID + "|" + method + "|" + path
			actual[target] = struct{}{}
			authorization, err := iamAuthorizationOf(operation)
			require.NoError(t, err, location)
			if authorization.permission != "" {
				_, declared := registrar.Registry().Find(authorization.permission)
				require.True(t, declared, "%s uses undeclared permission %q", location, authorization.permission)
			}
			actualAuthorizations[operation.OperationID] = authorization
		}
	}

	assert.Equal(t, expected, actual)
	assert.Len(t, actual, iamHTTPMappingCount)

	expectedAuthorizations := loadIAMHTTPAuthorizations(t)
	assert.Len(t, actualAuthorizations, iamHTTPMappingCount)
	for operationID, expectedAuthorization := range expectedAuthorizations {
		actualAuthorization, exists := actualAuthorizations[operationID]
		if assert.True(t, exists, "approved operation %s is missing", operationID) {
			assert.Equal(t, expectedAuthorization, actualAuthorization, operationID)
		}
	}
	for operationID := range actualAuthorizations {
		assert.Contains(t, expectedAuthorizations, operationID, "operation has no approved authorization")
	}
	for _, mapping := range mappings {
		assert.Contains(t, expectedAuthorizations, mapping.targetOperation)
	}
}

func iamAuthorizationOf(operation *huma.Operation) (iamHTTPAuthorization, error) {
	if operation == nil {
		return iamHTTPAuthorization{}, fmt.Errorf("nil operation")
	}
	operatingPlatform := authz.IsOperatingPlatformOperation(operation)
	markers := []struct {
		class   string
		present bool
	}{
		{class: "public", present: authz.IsPublicOperation(operation)},
		{class: "authenticated", present: authz.IsAuthenticatedOperation(operation) && !operatingPlatform},
		{class: "operating-platform", present: operatingPlatform && !authz.IsAuthenticatedOperation(operation)},
		{class: "operating-platform-member", present: operatingPlatform && authz.IsAuthenticatedOperation(operation)},
		{class: "tenant-member", present: authz.IsTenantMemberOperation(operation)},
		{class: "platform", present: authz.IsPlatformOperation(operation)},
	}
	var markedClass string
	for _, marker := range markers {
		if !marker.present {
			continue
		}
		if markedClass != "" {
			return iamHTTPAuthorization{}, fmt.Errorf("has contradictory %s and %s authorization markers", markedClass, marker.class)
		}
		markedClass = marker.class
	}

	permission, guarded := authz.OperationGuard(operation)
	extensionPermission, extensionGuarded := operation.Extensions[authz.GuardExtensionKey].(string)
	if guarded != extensionGuarded || permission != extensionPermission {
		return iamHTTPAuthorization{}, fmt.Errorf("guard metadata %q does not match OpenAPI extension %q", permission, extensionPermission)
	}

	selectors := map[string]bool{}
	authorizationSelectors := map[string]string{
		"path|" + authz.OperatingPlatformPath: "Operating platform identifier",
		"path|" + authz.TenantPath:            "Tenant resource identifier",
		"path|" + authz.EntityPath:            "Entity resource identifier",
	}
	for _, parameter := range operation.Parameters {
		if parameter == nil {
			continue
		}
		key := strings.ToLower(parameter.In + "|" + parameter.Name)
		description, authorizationSelector := authorizationSelectors[key]
		if !authorizationSelector {
			continue
		}
		if parameter.Description != description {
			continue
		}
		if !parameter.Required {
			return iamHTTPAuthorization{}, fmt.Errorf("authorization selector %s is optional", key)
		}
		selectors[key] = true
	}

	tenantPath := selectors["path|"+authz.TenantPath]
	platformPath := selectors["path|"+authz.OperatingPlatformPath]
	entityPath := selectors["path|"+authz.EntityPath]
	selectorCount := len(selectors)
	ownedResource := authz.IsOwnedResourceOperation(operation)

	switch markedClass {
	case "operating-platform":
		if !guarded || selectorCount != 1 || !platformPath {
			return iamHTTPAuthorization{}, fmt.Errorf("operating-platform operation requires one guard and platform path selector")
		}
	case "operating-platform-member":
		if guarded || selectorCount != 1 || !platformPath {
			return iamHTTPAuthorization{}, fmt.Errorf("operating-platform-member operation requires exactly one platform path selector and no guard")
		}
	case "public", "authenticated":
		if guarded || selectorCount != 0 {
			return iamHTTPAuthorization{}, fmt.Errorf("%s operation has a guard or authorization selector", markedClass)
		}
	case "tenant-member":
		if ownedResource && selectorCount == 0 {
			markedClass = "owned-tenant-member"
		} else if guarded || selectorCount != 1 || !tenantPath {
			return iamHTTPAuthorization{}, fmt.Errorf("tenant-member operation does not have exactly one tenant path selector")
		}
	case "platform":
		if !guarded {
			return iamHTTPAuthorization{}, fmt.Errorf("platform operation does not have exactly one guard")
		}
	case "":
		if !guarded {
			return iamHTTPAuthorization{}, fmt.Errorf("operation has no authorization declaration")
		}
		switch {
		case selectorCount == 0 && ownedResource:
			markedClass = "owned-resource"
		case selectorCount == 1 && tenantPath:
			markedClass = "tenant"
		case selectorCount == 2 && tenantPath && entityPath:
			markedClass = "entity-path"
		default:
			return iamHTTPAuthorization{}, fmt.Errorf("guarded operation has an invalid tenant/entity selector set")
		}
	}
	if markedClass != "public" && len(operation.Security) == 0 {
		return iamHTTPAuthorization{}, fmt.Errorf("non-public %s operation has no security requirement", markedClass)
	}
	if markedClass == "authenticated" && operation.Metadata["authz.session-only"] == true {
		if len(operation.Security) != 1 || len(operation.Security[0]) != 1 {
			return iamHTTPAuthorization{}, fmt.Errorf("session-only operation advertises alternative authentication")
		}
		if _, ok := operation.Security[0]["sessionCookie"]; !ok {
			return iamHTTPAuthorization{}, fmt.Errorf("session-only operation does not require sessionCookie")
		}
		markedClass = "session-only"
	}
	return iamHTTPAuthorization{class: markedClass, permission: permission}, nil
}

func loadIAMHTTPMappings(t *testing.T) []iamHTTPMapping {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "iam_http_mappings.txt"))
	require.NoError(t, err)

	var mappings []iamHTTPMapping
	for lineNumber, raw := range strings.Split(string(contents), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " => ", 2)
		require.Len(t, parts, 2, "line %d", lineNumber+1)
		target := strings.SplitN(parts[1], "|", 3)
		require.Len(t, target, 3, "line %d", lineNumber+1)
		mappings = append(mappings, iamHTTPMapping{
			sourceOperation: parts[0], targetOperation: target[0], method: target[1], path: target[2],
		})
	}
	return mappings
}

func loadIAMHTTPAuthorizations(t *testing.T) map[string]iamHTTPAuthorization {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "iam_http_guards.txt"))
	require.NoError(t, err)

	authorizations := make(map[string]iamHTTPAuthorization, iamHTTPMappingCount)
	for lineNumber, raw := range strings.Split(string(contents), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " => ", 2)
		require.Len(t, parts, 2, "line %d", lineNumber+1)
		fields := strings.Split(parts[1], "|")
		require.Contains(t, []string{"public", "authenticated", "session-only", "tenant-member", "owned-tenant-member", "tenant", "platform", "operating-platform", "operating-platform-member", "entity-path", "owned-resource"}, fields[0], "line %d", lineNumber+1)
		permissionRequired := fields[0] == "tenant" || fields[0] == "platform" || fields[0] == "operating-platform" || fields[0] == "owned-resource" || strings.HasPrefix(fields[0], "entity-")
		if permissionRequired {
			require.Len(t, fields, 2, "line %d", lineNumber+1)
			require.NotEmpty(t, fields[1], "line %d", lineNumber+1)
		} else {
			require.Len(t, fields, 1, "line %d", lineNumber+1)
		}
		_, duplicate := authorizations[parts[0]]
		require.False(t, duplicate, "duplicate authorization for %s", parts[0])
		authorization := iamHTTPAuthorization{class: fields[0]}
		if permissionRequired {
			authorization.permission = fields[1]
		}
		authorizations[parts[0]] = authorization
	}
	require.Len(t, authorizations, iamHTTPMappingCount)
	return authorizations
}

func openAPIPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	return map[string]*huma.Operation{
		"GET": item.Get, "PUT": item.Put, "POST": item.Post, "DELETE": item.Delete,
		"OPTIONS": item.Options, "HEAD": item.Head, "PATCH": item.Patch, "TRACE": item.Trace,
	}
}

func isIAMOperation(operationID string) bool {
	for _, prefix := range []string{
		"iam-", "authn-", "identity-", "oauth-", "organization-",
		"audit-", "governance-", "provisioning-", "scim-", "federation-", "webhook-",
	} {
		if strings.HasPrefix(operationID, prefix) {
			return true
		}
	}
	return false
}

// composedOpenAPIOperation is one operation of the composed application
// document, with the method and path it is published under.
type composedOpenAPIOperation struct {
	method    string
	path      string
	operation *huma.Operation
}

func (o composedOpenAPIOperation) String() string {
	return o.method + " " + o.path + " (" + o.operation.OperationID + ")"
}

// composedOpenAPI registers every module of the composed application and
// returns the document it publishes together with its operations.
func composedOpenAPI(t *testing.T) (*huma.OpenAPI, []composedOpenAPIOperation) {
	t.Helper()
	app := bootstrapComposedApp(t)
	api := humax.New(chi.NewRouter(), humax.Config{Name: app.config.Name, Version: app.config.Version})
	require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
	var operations []composedOpenAPIOperation
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range openAPIPathOperations(item) {
			if operation != nil {
				operations = append(operations, composedOpenAPIOperation{method: method, path: path, operation: operation})
			}
		}
	}
	require.NotEmpty(t, operations)
	return api.OpenAPI(), operations
}

// TestComposedOpenAPIAddressesPrincipalsByPrincipalID keeps one name for one
// identifier: a principal in a path is principal_id on every route (D75/D81),
// never a synonym that makes parallel routes read as different resources.
func TestComposedOpenAPIAddressesPrincipalsByPrincipalID(t *testing.T) {
	_, operations := composedOpenAPI(t)
	for _, entry := range operations {
		for _, parameter := range entry.operation.Parameters {
			if parameter.In == "path" {
				assert.NotEqual(t, "subject", parameter.Name, "%s names a principal subject; use principal_id", entry)
			}
		}
	}
}

// nonSnowflakePathIDs are the path parameters whose name ends in _id but whose
// value is not a platform snowflake ID. Every other *_id parameter must publish
// the snowflake schema, so a client can validate an identifier before it sends
// it and the server rejects a malformed one before any handler runs.
var nonSnowflakePathIDs = map[string]string{
	"inbox_item_id": "an inbox item is addressed by KIND:ID, not by a standalone row ID",
	"link_id":       "an identity link is addressed by provider_id:base64url(subject), the upstream identity it names",
	"candidate_id":  "an avatar candidate is a 48-hex content token that is never stored as a row",
}

func TestComposedOpenAPIPublishesSnowflakePathIDs(t *testing.T) {
	_, operations := composedOpenAPI(t)
	seen := map[string]bool{}
	for _, entry := range operations {
		for _, parameter := range entry.operation.Parameters {
			if parameter.In != "path" || !strings.HasSuffix(parameter.Name, "_id") {
				continue
			}
			if _, excluded := nonSnowflakePathIDs[parameter.Name]; excluded {
				seen[parameter.Name] = true
				if parameter.Name == "inbox_item_id" {
					require.NotNil(t, parameter.Schema)
					require.NotNil(t, parameter.Schema.MaxLength)
					assert.Equal(t, 100, *parameter.Schema.MaxLength)
				}
				continue
			}
			if assert.NotNil(t, parameter.Schema, "%s: %s has no schema", entry, parameter.Name) {
				assert.Equal(t, `^[1-9][0-9]*$`, parameter.Schema.Pattern, "%s: %s is a snowflake ID", entry, parameter.Name)
				if assert.NotNil(t, parameter.Schema.MaxLength, "%s: %s has no maxLength", entry, parameter.Name) {
					assert.Equal(t, 19, *parameter.Schema.MaxLength, "%s: %s is a snowflake ID", entry, parameter.Name)
				}
			}
		}
	}
	for name := range nonSnowflakePathIDs {
		assert.True(t, seen[name], "exclusion %s is no longer published; delete it", name)
	}
}

// resolveSchema follows a component reference to the schema it names.
func resolveSchema(document *huma.OpenAPI, schema *huma.Schema) *huma.Schema {
	if schema != nil && schema.Ref != "" {
		return document.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	return schema
}

func TestComposedOpenAPIProjectListContract(t *testing.T) {
	document, _ := composedOpenAPI(t)
	operation := document.Paths["/tenants/{tenant_id}/projects"].Get
	require.NotNil(t, operation)
	require.Equal(t, "project.list", operation.OperationID)
	data := successData(document, operation)["200"]
	require.NotNil(t, data)
	require.NotNil(t, data.Items)
	list := resolveSchema(document, data.Items)
	require.NotNil(t, list)
	fields := []string{"project_id", "code", "name", "client", "stage", "team_mode", "health", "health_reasons", "budget", "target_date", "lead_agent_id", "version", "updated_at", "counts", "archived_at"}
	actual := make([]string, 0, len(list.Properties))
	for name := range list.Properties {
		actual = append(actual, name)
	}
	assert.ElementsMatch(t, fields, actual)
	assert.ElementsMatch(t, fields, list.Required)

	// Inspect serialized schema bytes, as consumed by client generators.
	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	var published map[string]any
	require.NoError(t, json.Unmarshal(encoded, &published))
	// PRD P10.5/REQ-CORE-013: archived-list entries retain their archive marker.
	archivedAt, ok := resolveOpenAPIPointer(published, data.Items.Ref+"/properties/archived_at")
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": []any{"string", "null"}, "format": "date-time"}, archivedAt)
	client, ok := resolveOpenAPIPointer(published, data.Items.Ref+"/properties/client")
	require.True(t, ok)
	require.Equal(t, map[string]any{"anyOf": []any{
		map[string]any{"$ref": "#/components/schemas/ProjectClientView"},
		map[string]any{"type": "null"},
	}}, client)
	budget := resolveSchema(document, list.Properties["budget"])
	require.NotNil(t, budget)
	require.NotNil(t, budget.Properties["cap"])
	require.NotNil(t, budget.Properties["spent"])
	capType, ok := resolveOpenAPIPointer(published, list.Properties["budget"].Ref+"/properties/cap/type")
	require.True(t, ok)
	assert.Equal(t, []any{"string", "null"}, capType)
	spentType, ok := resolveOpenAPIPointer(published, list.Properties["budget"].Ref+"/properties/spent/type")
	require.True(t, ok)
	assert.Equal(t, "string", spentType)
}

// successData returns the data schema of each 2xx JSON envelope the operation
// answers with.
func successData(document *huma.OpenAPI, operation *huma.Operation) map[string]*huma.Schema {
	found := map[string]*huma.Schema{}
	for status, response := range operation.Responses {
		if !strings.HasPrefix(status, "2") || response == nil {
			continue
		}
		media := response.Content["application/json"]
		if media == nil {
			continue
		}
		envelope := resolveSchema(document, media.Schema)
		if envelope == nil || envelope.Properties["data"] == nil {
			continue
		}
		found[status] = resolveSchema(document, envelope.Properties["data"])
	}
	return found
}

// TestComposedOpenAPIListsPageThroughTheEnvelope keeps one list shape: a
// collection answers with its items as data and its paging in meta.page
// (respx.List). A data object carrying its own items and total is a second
// paging contract the shared client helpers cannot read.
func TestComposedOpenAPIListsPageThroughTheEnvelope(t *testing.T) {
	document, operations := composedOpenAPI(t)
	checked := 0
	for _, entry := range operations {
		for status, data := range successData(document, entry.operation) {
			checked++
			if data == nil || data.Properties == nil {
				continue
			}
			_, items := data.Properties["items"]
			_, total := data.Properties["total"]
			// An access review carrying its own items and counters is a resource,
			// not a page; a page is an object that is nothing but the two.
			assert.False(t, items && total && len(data.Properties) == 2, "%s answers %s with a self-paged {items,total} object; use respx.List", entry, status)
		}
	}
	assert.Greater(t, checked, 100, "the walk must reach the enveloped responses")
}

// acceptedCreates answer 202 instead of 201: the request is accepted without
// revealing whether anything was created, because the answer must read the same
// for an address that exists and one that does not.
var acceptedCreates = map[string]string{
	"authn-create-registration":               "self-registration never reveals whether the address is taken",
	"authn-create-email-verification-request": "a verification mail is queued without confirming the address",
	"authn-create-password-recovery-request":  "a recovery mail is queued without confirming the account",
	"authn-create-phone-verification-request": "a verification message is queued without confirming the number",
	"authn-create-phone-login-request":        "a sign-in code is queued without confirming the number reaches an account",
	"authn-create-mfa-verification-request":   "a second-factor code is queued without confirming the challenge is answerable that way",
}

// TestComposedOpenAPICreatesAnswer201 keeps one success status for creation: an
// operation whose ID says it creates answers 201 Created, whichever module or
// registrar declared it.
func TestComposedOpenAPICreatesAnswer201(t *testing.T) {
	_, operations := composedOpenAPI(t)
	creates := 0
	for _, entry := range operations {
		if entry.method != "POST" || !strings.Contains(entry.operation.OperationID, "create") {
			continue
		}
		creates++
		want := "201"
		if _, accepted := acceptedCreates[entry.operation.OperationID]; accepted {
			want = "202"
		}
		var success []string
		for status := range entry.operation.Responses {
			if strings.HasPrefix(status, "2") {
				success = append(success, status)
			}
		}
		slices.Sort(success)
		expected := []string{want}
		if entry.operation.OperationID == "organization-create-operating-platform" {
			// Retired creation remains guarded for compatibility and always conflicts.
			assert.True(t, entry.operation.Deprecated)
			assert.Contains(t, entry.operation.Responses, "409")
			expected = nil
		}
		if entry.operation.OperationID == "knowledge.create" {
			// Spec06 BR-06-35 / AC-06-22: duplicate memory returns its existing entry.
			expected = []string{"200", "201"}
		}
		assert.Equal(t, expected, success, "%s is a create operation", entry)
	}
	assert.Greater(t, creates, 60, "the walk must reach the create operations")
}

// TestComposedOpenAPINamesTheScopeEnumScopeType keeps one public name for the
// system/platform/tenant/entity scope enum. Storage keeps its scope_kind column;
// a schema publishing that name beside scope_type would make one value read as
// two different fields.
func TestComposedOpenAPINamesTheScopeEnumScopeType(t *testing.T) {
	document, _ := composedOpenAPI(t)
	schemas := document.Components.Schemas.Map()
	require.NotEmpty(t, schemas)
	for name, schema := range schemas {
		_, published := schema.Properties["scope_kind"]
		assert.False(t, published, "%s publishes scope_kind; the API field is scope_type", name)
	}
}

// TestComposedOpenAPIDeclaresAnonymousAccessOneWay keeps one spelling for an
// operation anyone may call: it declares no security at all. The document has no
// global requirement, so an explicit empty list means the same thing and only
// makes two anonymous operations look different. An anonymous operation that
// still reads a credential names it, so a client knows to send it.
func TestComposedOpenAPIDeclaresAnonymousAccessOneWay(t *testing.T) {
	document, operations := composedOpenAPI(t)
	require.Empty(t, document.Security, "a global requirement would change what an omitted one means")
	anonymous, logout := 0, 0
	for _, entry := range operations {
		if entry.operation.OperationID == "authn-delete-current-session" {
			logout++
			assert.Equal(t, []map[string][]string{{"sessionCookie": {}}}, entry.operation.Security,
				"%s reads the session cookie it destroys", entry)
		}
		if entry.operation.Security == nil {
			anonymous++
			continue
		}
		assert.NotEmpty(t, entry.operation.Security, "%s declares an empty security list; omit it", entry)
	}
	assert.Equal(t, 1, logout, "the logout operation must be published")
	assert.Greater(t, anonymous, 20, "the walk must reach the anonymous operations")
}

// TestComposedOpenAPIPagesListsOneWay holds every offset-paged collection to
// the list-parameter convention (decisions: List Parameters): limit defaults to
// 50 and caps at 200, offset defaults to 0, and a free-text filter is named
// search. Cursor-paged collections (those taking after) keep their own bounds by
// design: they page a live tree, not a counted table.
func TestComposedOpenAPIPagesListsOneWay(t *testing.T) {
	_, operations := composedOpenAPI(t)
	paged := 0
	for _, entry := range operations {
		if entry.operation.OperationID == "inbox_events.list" {
			parameters := map[string]*huma.Param{}
			for _, p := range entry.operation.Parameters {
				parameters[p.Name] = p
			}
			require.NotNil(t, parameters["since"])
			require.NotNil(t, parameters["limit"])
			assert.Nil(t, parameters["offset"])
			assert.InDelta(t, 100, *parameters["limit"].Schema.Maximum, 0)
			continue
		}

		query := map[string]*huma.Param{}
		for _, parameter := range entry.operation.Parameters {
			if parameter.In == "query" {
				query[parameter.Name] = parameter
			}
		}
		assert.NotContains(t, query, "q", "%s names its free-text filter q; use search", entry)
		if query["cursor"] != nil {
			// Retained the capability spec exceptions (no offset parameter may coexist):
			// knowledge.list: spec06 §6 A38 and §15 / spec00 §16.10.
			// knowledge.sources.chunks.list: spec06 §6 A10, superseded by §15.
			// Both retain limit default 50 / maximum 100; new the capability lists use offset 50/200.
			assert.NotContains(t, query, "offset", "%s uses the cursor contract", entry)
			require.NotNil(t, query["limit"], "%s cursor list must be bounded", entry)
			assert.InDelta(t, 100, *query["limit"].Schema.Maximum, 0, "%s cursor limit maximum", entry)
			assert.EqualValues(t, 50, query["limit"].Schema.Default, "%s cursor limit default", entry)
			continue
		}
		limit := query["limit"]
		if limit == nil || query["after"] != nil {
			continue
		}
		paged++
		if assert.NotNil(t, limit.Schema, "%s: limit has no schema", entry) {
			assert.EqualValues(t, 50, limit.Schema.Default, "%s: limit default", entry)
			if assert.NotNil(t, limit.Schema.Minimum, "%s: limit minimum", entry) {
				assert.InDelta(t, 1, *limit.Schema.Minimum, 0, "%s: limit minimum", entry)
			}
			if assert.NotNil(t, limit.Schema.Maximum, "%s: limit maximum", entry) {
				maximum := 200
				// Specs 07 and 02 cap project, requirement and assignment lists at 100.
				if entry.operation.OperationID == "project.list" || entry.operation.OperationID == "requirement.list" || entry.operation.OperationID == "assignment.list" {
					maximum = 100
				}
				assert.InDelta(t, maximum, *limit.Schema.Maximum, 0, "%s: limit maximum", entry)
			}
		}
		offset := query["offset"]
		if assert.NotNil(t, offset, "%s pages by limit without offset", entry) && assert.NotNil(t, offset.Schema) {
			assert.EqualValues(t, 0, offset.Schema.Default, "%s: offset default", entry)
		}
	}
	assert.Greater(t, paged, 50, "the walk must reach the paged collections")
}

// publishedOpenAPIEnv names a document to check instead of the committed one.
// openapi-check points it at the document it has just generated, so an invalid
// document is reported as invalid rather than only as different.
const publishedOpenAPIEnv = "OPENAPI_DOCUMENT"

// publishedOpenAPI reads the published document as plain JSON: the checks below
// hold the bytes a client generator reads, not huma's in-memory model, which
// can say things the serialization does not.
func publishedOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	path := os.Getenv(publishedOpenAPIEnv)
	if path == "" {
		path = filepath.Join("..", "..", "docs", "openapi.json")
	}
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(contents, &document), path)
	return document
}

// TestPublishedOpenAPICoversEveryRuntimeRoute holds the published document to
// the runtime route inventory. The document is exported with a fixed set of
// modules enabled; one left disabled there drops its routes from the contract
// while the service still serves them. Every mapping line is checked, so a new
// route joins the check by joining the inventory.
func TestPublishedOpenAPICoversEveryRuntimeRoute(t *testing.T) {
	paths, _ := publishedOpenAPI(t)["paths"].(map[string]any)
	require.NotEmpty(t, paths)
	mappings := loadIAMHTTPMappings(t)
	require.NotEmpty(t, mappings)
	for _, mapping := range mappings {
		item, _ := paths[mapping.path].(map[string]any)
		assert.Contains(t, item, strings.ToLower(mapping.method),
			"%s %s (%s) is served but not published", mapping.method, mapping.path, mapping.targetOperation)
	}
}

func TestComposedOpenAPIPathParametersMatchTemplates(t *testing.T) {
	contents, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(contents, &document))
	type parameter struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	}
	template := regexp.MustCompile(`\{([^{}]+)\}`)
	operations := 0
	for path, item := range document.Paths {
		var shared []parameter
		if raw, ok := item["parameters"]; ok {
			require.NoError(t, json.Unmarshal(raw, &shared))
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"} {
			raw, ok := item[method]
			if !ok {
				continue
			}
			operations++
			var operation struct {
				Parameters []parameter `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(raw, &operation))
			parameters := make(map[string]bool)
			for _, p := range slices.Concat(shared, operation.Parameters) {
				if p.In == "path" {
					parameters[p.Name] = p.Required
				}
			}
			variables := make(map[string]bool)
			for _, match := range template.FindAllStringSubmatch(path, -1) {
				variables[match[1]] = true
				assert.True(t, parameters[match[1]], "%s %s must declare required path parameter %s", method, path, match[1])
			}
			for name := range parameters {
				assert.True(t, variables[name], "%s %s declares path parameter %s absent from its template", method, path, name)
			}
		}
	}
	assert.Positive(t, operations)
}

// componentName is the OpenAPI 3.1 rule for every key under components.
var componentName = regexp.MustCompile(`^[a-zA-Z0-9.\-_]+$`)

// TestPublishedOpenAPIIsWellFormed checks the structural rules this document
// has actually broken and that no other gate reads: component names a Go type
// literal leaked into, response headers declared as parameters, and references
// that point at nothing. Each of them passes JSON parsing and the type
// generator while making the document invalid for stricter tools.
func TestPublishedOpenAPIIsWellFormed(t *testing.T) {
	document := publishedOpenAPI(t)

	components, _ := document["components"].(map[string]any)
	require.NotEmpty(t, components)
	for kind, entries := range components {
		named, _ := entries.(map[string]any)
		for name := range named {
			assert.Regexp(t, componentName, name, "components.%s has a name OpenAPI forbids", kind)
		}
	}

	headers, refs := 0, 0
	walkOpenAPI(document, "#", func(location string, node map[string]any) {
		if responseHeaders, ok := node["headers"].(map[string]any); ok && strings.Contains(location, "/responses/") {
			for name, raw := range responseHeaders {
				header, _ := raw.(map[string]any)
				headers++
				assert.NotContains(t, header, "name", "%s/headers/%s: a header object takes its name from the key", location, name)
				assert.NotContains(t, header, "in", "%s/headers/%s: a header object has no location", location, name)
			}
		}
		if ref, ok := node["$ref"].(string); ok {
			refs++
			_, resolved := resolveOpenAPIPointer(document, ref)
			assert.True(t, resolved, "%s: $ref %q resolves to nothing", location, ref)
		}
	})
	assert.Positive(t, headers, "the walk must reach the response headers")
	assert.Greater(t, refs, 100, "the walk must reach the references")
}

// walkOpenAPI visits every object in the document with its JSON pointer.
func walkOpenAPI(node any, location string, visit func(string, map[string]any)) {
	switch value := node.(type) {
	case map[string]any:
		visit(location, value)
		for key, child := range value {
			walkOpenAPI(child, location+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(key), visit)
		}
	case []any:
		for index, child := range value {
			walkOpenAPI(child, location+"/"+strconv.Itoa(index), visit)
		}
	}
}

// resolveOpenAPIPointer follows a local reference. The document is published as
// one file, so a reference to anything outside it resolves to nothing.
func resolveOpenAPIPointer(document map[string]any, ref string) (any, bool) {
	if ref == "#" {
		return document, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = document
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
		switch value := current.(type) {
		case map[string]any:
			child, ok := value[token]
			if !ok {
				return nil, false
			}
			current = child
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(value) {
				return nil, false
			}
			current = value[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// TestResolveOpenAPIPointerFollowsOnlyLocalReferences pins the resolver the
// well-formedness check relies on, so a broken resolver cannot pass every
// reference.
func TestResolveOpenAPIPointerFollowsOnlyLocalReferences(t *testing.T) {
	document := map[string]any{
		"components": map[string]any{"schemas": map[string]any{"A/B~C": map[string]any{"type": "object"}}},
		"tags":       []any{map[string]any{"name": "first"}},
	}
	for ref, want := range map[string]bool{
		"#": true, "#/components/schemas/A~1B~0C": true, "#/tags/0/name": true,
		"#/components/schemas/Missing": false, "#/tags/1": false, "#/tags/x": false,
		"#/tags/0/name/deeper": false, "other.json#/components": false,
	} {
		_, resolved := resolveOpenAPIPointer(document, ref)
		assert.Equal(t, want, resolved, ref)
	}
}
