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

const iamHTTPMappingCount = 232

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
	authz.RegisterTenantMember(registrar, api, huma.Operation{OperationID: "tenant-member", Method: http.MethodGet, Path: "/tenant-member"}, handler)
	authz.Register(registrar, api, huma.Operation{OperationID: "tenant", Method: http.MethodGet, Path: "/tenant"}, authz.Guard{Resource: "store", Verb: "view"}, handler)
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "platform", Method: http.MethodGet, Path: "/platform"}, authz.Guard{Resource: "tenant", Verb: "view"}, handler)
	authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "entity-header", Method: http.MethodGet, Path: "/entity-header"}, authz.Guard{Resource: "store", Verb: "view"}, handler)
	authz.RegisterEntityAdapter(registrar, api, huma.Operation{OperationID: "entity-query", Method: http.MethodGet, Path: "/entity-query"}, authz.Guard{Resource: "store", Verb: "view"}, func(huma.Context) {})

	expected := map[string]iamHTTPAuthorization{
		"/public":        {class: "public"},
		"/authenticated": {class: "authenticated"},
		"/tenant-member": {class: "tenant-member"},
		"/tenant":        {class: "tenant", permission: "store_view"},
		"/platform":      {class: "platform", permission: "tenant_view"},
		"/entity-header": {class: "entity-header", permission: "store_view"},
		"/entity-query":  {class: "entity-query", permission: "store_view"},
	}
	for path, want := range expected {
		got, err := iamAuthorizationOf(api.OpenAPI().Paths[path].Get)
		require.NoError(t, err, path)
		assert.Equal(t, want, got, path)
	}

	tenant := api.OpenAPI().Paths["/tenant"].Get
	tenant.Extensions[authz.GuardExtensionKey] = "role_view"
	_, err := iamAuthorizationOf(tenant)
	assert.ErrorContains(t, err, "does not match")

	authenticated := api.OpenAPI().Paths["/authenticated"].Get
	authenticated.Security = nil
	_, err = iamAuthorizationOf(authenticated)
	assert.ErrorContains(t, err, "has no security requirement")

	entity := api.OpenAPI().Paths["/entity-header"].Get
	for _, parameter := range entity.Parameters {
		if parameter.Name == authz.EntityHeader && parameter.In == "header" {
			parameter.Required = false
		}
	}
	_, err = iamAuthorizationOf(entity)
	assert.ErrorContains(t, err, "is optional")
}

func TestComposedIAMOpenAPIHasExactMappedOperationSet(t *testing.T) {
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
	}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { assert.NoError(t, app.shutdown()) })

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
		for method, operation := range openAPIPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			if previous, exists := operationIDs[operation.OperationID]; exists {
				t.Errorf("duplicate OperationID %q at %s and %s", operation.OperationID, previous, location)
			}
			operationIDs[operation.OperationID] = location
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
	markers := []struct {
		class   string
		present bool
	}{
		{class: "public", present: authz.IsPublicOperation(operation)},
		{class: "authenticated", present: authz.IsAuthenticatedOperation(operation)},
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
		"header|" + strings.ToLower(authz.TenantHeader): "Tenant authorization boundary",
		"header|" + strings.ToLower(authz.EntityHeader): "Authorized entity resource boundary",
		"query|" + authz.TenantQuery:                    "Tenant authorization selector",
		"query|" + authz.EntityQuery:                    "Entity authorization selector",
	}
	for _, parameter := range operation.Parameters {
		if parameter == nil {
			continue
		}
		key := strings.ToLower(parameter.In + "|" + parameter.Name)
		description, authorizationSelector := authorizationSelectors[key]
		if !authorizationSelector || parameter.Description != description {
			continue
		}
		if !parameter.Required {
			return iamHTTPAuthorization{}, fmt.Errorf("authorization selector %s is optional", key)
		}
		selectors[key] = true
	}

	tenantHeader := selectors["header|"+strings.ToLower(authz.TenantHeader)]
	entityHeader := selectors["header|"+strings.ToLower(authz.EntityHeader)]
	tenantQuery := selectors["query|"+authz.TenantQuery]
	entityQuery := selectors["query|"+authz.EntityQuery]
	selectorCount := len(selectors)

	switch markedClass {
	case "public", "authenticated":
		if guarded || selectorCount != 0 {
			return iamHTTPAuthorization{}, fmt.Errorf("%s operation has a guard or authorization selector", markedClass)
		}
	case "tenant-member":
		if guarded || selectorCount != 1 || !tenantHeader {
			return iamHTTPAuthorization{}, fmt.Errorf("tenant-member operation does not have exactly one tenant header selector")
		}
	case "platform":
		if !guarded || selectorCount != 0 {
			return iamHTTPAuthorization{}, fmt.Errorf("platform operation does not have exactly one guard and no tenant/entity selector")
		}
	case "":
		if !guarded {
			return iamHTTPAuthorization{}, fmt.Errorf("operation has no authorization declaration")
		}
		switch {
		case selectorCount == 1 && tenantHeader:
			markedClass = "tenant"
		case selectorCount == 2 && tenantHeader && entityHeader:
			markedClass = "entity-header"
		case selectorCount == 2 && tenantQuery && entityQuery:
			markedClass = "entity-query"
		default:
			return iamHTTPAuthorization{}, fmt.Errorf("guarded operation has an invalid tenant/entity selector set")
		}
	}
	if markedClass != "public" && len(operation.Security) == 0 {
		return iamHTTPAuthorization{}, fmt.Errorf("non-public %s operation has no security requirement", markedClass)
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
		require.Contains(t, []string{"public", "authenticated", "tenant-member", "tenant", "platform", "entity-header", "entity-query"}, fields[0], "line %d", lineNumber+1)
		permissionRequired := fields[0] == "tenant" || fields[0] == "platform" || strings.HasPrefix(fields[0], "entity-")
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
		"audit-", "governance-", "provisioning-", "scim-", "federation-",
	} {
		if strings.HasPrefix(operationID, prefix) {
			return true
		}
	}
	return false
}
