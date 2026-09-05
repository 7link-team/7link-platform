package core

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const coreRouteManifest = `
iam-list-relationships|GET|/iam/relationships
iam-put-relationship|POST|/iam/relationships
iam-delete-relationship|DELETE|/iam/relationships
iam-check-authorization|POST|/iam/authorization/check
iam-platform-permission-catalog|GET|/iam/platform-permission-catalog
iam-list-platform-administrators|GET|/iam/platform-administrators
iam-put-platform-administrator|PUT|/iam/platform-administrators/{principal_id}
iam-delete-platform-administrator|DELETE|/iam/platform-administrators/{principal_id}
iam-permission-catalog|GET|/iam/permission-catalog
iam-scope-model|GET|/iam/scope-model
iam-menu-catalog|GET|/iam/menu-catalog
iam-authorization-constraint|POST|/iam/authorization/constraints
iam-explain-authorization|POST|/iam/authorization/explain
iam-list-entities|GET|/iam/entities
iam-create-entity|POST|/iam/entities
iam-get-entity|GET|/iam/entities/{entity_id}
iam-update-entity|PATCH|/iam/entities/{entity_id}
iam-delete-entity|DELETE|/iam/entities/{entity_id}
iam-list-entity-role-bindings|GET|/iam/entities/{entity_id}/role-bindings
iam-put-entity-role-binding|PUT|/iam/entities/{entity_id}/role-bindings/{role_id}/{principal_id}
iam-delete-entity-role-binding|DELETE|/iam/entities/{entity_id}/role-bindings/{role_id}/{principal_id}
iam-list-roles|GET|/iam/roles
iam-create-role|POST|/iam/roles
iam-get-role|GET|/iam/roles/{role_id}
iam-get-role-data-scope|GET|/iam/roles/{role_id}/data-scope
iam-set-role-data-scope|PUT|/iam/roles/{role_id}/data-scope
iam-update-role|PATCH|/iam/roles/{role_id}
iam-delete-role|DELETE|/iam/roles/{role_id}
iam-list-role-permissions|GET|/iam/roles/{role_id}/permissions
iam-list-role-permission-grants|GET|/iam/roles/{role_id}/permission-grants
iam-grant-role-permission|PUT|/iam/roles/{role_id}/permissions/{permission_code}
iam-revoke-role-permission|DELETE|/iam/roles/{role_id}/permissions/{permission_code}
iam-set-role-permission-condition|PUT|/iam/roles/{role_id}/permissions/{permission_code}/condition
iam-clear-role-permission-condition|DELETE|/iam/roles/{role_id}/permissions/{permission_code}/condition
iam-list-role-members|GET|/iam/roles/{role_id}/members
iam-add-role-member|PUT|/iam/roles/{role_id}/members/{subject}
iam-remove-role-member|DELETE|/iam/roles/{role_id}/members/{subject}
iam-list-role-directory-bindings|GET|/iam/roles/{role_id}/directory-bindings
iam-add-role-directory-binding|PUT|/iam/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}
iam-remove-role-directory-binding|DELETE|/iam/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}
iam-list-tenant-members|GET|/iam/members
iam-create-tenant-member|POST|/iam/members
iam-get-tenant-member|GET|/iam/members/{subject}
iam-update-tenant-member|PATCH|/iam/members/{subject}
iam-list-tenant-member-roles|GET|/iam/members/{subject}/roles
iam-list-menus|GET|/iam/menus
iam-create-menu|POST|/iam/menus
iam-get-menu|GET|/iam/menus/{menu_id}
iam-update-menu|PATCH|/iam/menus/{menu_id}
iam-delete-menu|DELETE|/iam/menus/{menu_id}
iam-effective-menus|GET|/iam/me/menus`

func declarationAPI(t *testing.T) huma.API {
	t.Helper()
	return newHTTPIAMHarness(t, true).api
}

func TestCoreRouteManifestMatchesGeneratedOpenAPI(t *testing.T) {
	api := declarationAPI(t)
	want := strings.Fields(coreRouteManifest)
	got := operationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 51)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))
}

func TestCoreOpenAPISelectorsAndParametersAreConsistent(t *testing.T) {
	spec := declarationAPI(t).OpenAPI()
	operationIDs := make(map[string]string, 51)
	pathParameter := regexp.MustCompile(`\{([^}]+)\}`)

	for path, item := range spec.Paths {
		assert.Equal(t, strings.ToLower(path), path, "paths use lowercase resource names")
		assert.False(t, strings.HasSuffix(path, "/"), "paths have no trailing slash")
		for method, operation := range pathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			require.NotEmpty(t, operation.OperationID, location)
			if previous, duplicate := operationIDs[operation.OperationID]; duplicate {
				t.Fatalf("duplicate operationId %q on %s and %s", operation.OperationID, previous, location)
			}
			operationIDs[operation.OperationID] = location
			assert.Equal(t, []string{"iam"}, operation.Tags, location)
			assert.NotEmpty(t, operation.Security, location)

			parameters := make(map[string]*huma.Param, len(operation.Parameters))
			for _, parameter := range operation.Parameters {
				key := strings.ToLower(parameter.In + "\x00" + parameter.Name)
				if _, duplicate := parameters[key]; duplicate {
					t.Errorf("%s declares duplicate %s parameter %q", location, parameter.In, parameter.Name)
				}
				parameters[key] = parameter
			}

			tenantKey := strings.ToLower("header\x00" + authz.TenantHeader)
			if authz.IsPlatformOperation(operation) {
				assert.NotContains(t, parameters, tenantKey, "%s is platform-scoped", location)
			} else {
				tenant := parameters[tenantKey]
				if assert.NotNil(t, tenant, "%s is tenant-scoped", location) {
					assert.True(t, tenant.Required, location)
					assert.Equal(t, `^[1-9][0-9]*$`, tenant.Schema.Pattern, location)
				}
			}

			declaredPathParams := map[string]*huma.Param{}
			for _, parameter := range operation.Parameters {
				if parameter.In == "path" {
					declaredPathParams[parameter.Name] = parameter
					assert.True(t, parameter.Required, "%s path parameter %q", location, parameter.Name)
					if isIDPathParameter(parameter.Name) {
						assert.Equal(t, huma.TypeString, parameter.Schema.Type, location)
						assert.Equal(t, `^[1-9][0-9]*$`, parameter.Schema.Pattern, location)
						require.NotNil(t, parameter.Schema.MaxLength, location)
						assert.Equal(t, 19, *parameter.Schema.MaxLength, location)
					}
				}
			}
			placeholders := pathParameter.FindAllStringSubmatch(path, -1)
			require.Len(t, declaredPathParams, len(placeholders), "%s path parameter set", location)
			for _, placeholder := range placeholders {
				assert.Contains(t, declaredPathParams, placeholder[1], location)
			}
		}
	}
}

func TestCoreOpenAPIRelationshipTupleAndPatchContracts(t *testing.T) {
	spec := declarationAPI(t).OpenAPI()
	deleteRelationship := spec.Paths["/iam/relationships"].Delete
	require.NotNil(t, deleteRelationship)

	parameters := make(map[string]*huma.Param, len(deleteRelationship.Parameters))
	for _, parameter := range deleteRelationship.Parameters {
		parameters[parameter.In+"\x00"+parameter.Name] = parameter
	}
	for _, name := range []string{"subject_type", "subject_id", "relation", "resource_type", "resource_id"} {
		parameter := parameters["query\x00"+name]
		if assert.NotNil(t, parameter, name) {
			assert.True(t, parameter.Required, name)
		}
	}
	assert.False(t, parameters["query\x00entity_id"].Required)
	assert.False(t, parameters["query\x00subject_relation"].Required)

	// A create and the update that follows it name the same optional reference,
	// so they have to accept the same values for it. A form that offers "no
	// parent" as a cleared select sends an empty string either way, and a create
	// that rejects what the update accepts turns that into a 422 nobody reading
	// the two schemas side by side would predict.
	for _, test := range []struct {
		collection string
		resource   string
		property   string
	}{
		{collection: "/iam/menus", resource: "/iam/menus/{menu_id}", property: "parent_id"},
		{collection: "/iam/entities", resource: "/iam/entities/{entity_id}", property: "parent_id"},
		{collection: "/iam/members", resource: "/iam/members/{subject}", property: "department_id"},
	} {
		create := requestProperty(t, spec, spec.Paths[test.collection].Post, test.property)
		update := requestProperty(t, spec, spec.Paths[test.resource].Patch, test.property)
		assert.Equal(t, `^([1-9][0-9]*)?$`, create.Pattern, test.collection+" "+test.property)
		assert.Equal(t, create.Pattern, update.Pattern, test.resource+" "+test.property)
		assert.NotEmpty(t, create.Description, test.collection+" "+test.property,
			"an optional reference has to say what omitting it means")
		assert.Contains(t, update.Description, "empty string", test.resource+" "+test.property)
	}
}

// requestProperty reads one property out of an operation JSON request body,
// following the component reference huma emits for a named body schema.
func requestProperty(t *testing.T, spec *huma.OpenAPI, operation *huma.Operation, name string) *huma.Schema {
	t.Helper()
	require.NotNil(t, operation)
	schema := operation.RequestBody.Content["application/json"].Schema
	if schema.Ref != "" {
		schema = spec.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	property := schema.Properties[name]
	require.NotNil(t, property, name)
	return property
}

func TestCoreOpenAPIUsesOneErrorEnvelopeAndDeclaresDomainStatuses(t *testing.T) {
	spec := declarationAPI(t).OpenAPI()
	for path, item := range spec.Paths {
		for method, operation := range pathOperations(item) {
			if operation == nil {
				continue
			}
			for status, response := range operation.Responses {
				if strings.HasPrefix(status, "2") {
					continue
				}
				media := response.Content["application/json"]
				if assert.NotNil(t, media, method+" "+path+" "+status) && assert.NotNil(t, media.Schema) {
					assert.Equal(t, "#/components/schemas/ErrorEnvelope", media.Schema.Ref, method+" "+path+" "+status)
				}
			}
		}
	}

	expected := map[string][]string{
		"iam-add-role-member":          {"404", "409", "422"},
		"iam-create-tenant-member":     {"404", "409", "422"},
		"iam-list-tenant-member-roles": {"404", "422"},
		"iam-create-menu":              {"409", "422"},
		"iam-get-menu":                 {"404", "422"},
		"iam-update-menu":              {"404", "409", "422"},
		"iam-delete-menu":              {"404", "409", "422"},
	}
	for _, item := range spec.Paths {
		for _, operation := range pathOperations(item) {
			if operation == nil || expected[operation.OperationID] == nil {
				continue
			}
			for _, status := range expected[operation.OperationID] {
				assert.Contains(t, operation.Responses, status, operation.OperationID)
			}
			delete(expected, operation.OperationID)
		}
	}
	assert.Empty(t, expected, "every expected operation must exist")
}

func isIDPathParameter(name string) bool {
	switch name {
	case "entity_id", "role_id", "principal_id", "menu_id", "subject", "assignee_id":
		return true
	default:
		return false
	}
}

func operationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 51)
	for path, item := range spec.Paths {
		for method, operation := range pathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func pathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	}
}
