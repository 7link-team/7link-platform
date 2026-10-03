package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const iamHTTPMappingCount = 357

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
	}}
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
