package app

import (
	"strings"
	"testing"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	"link_id":      "an identity link is addressed by provider_id:base64url(subject), the upstream identity it names",
	"candidate_id": "an avatar candidate is a 48-hex content token that is never stored as a row",
	"session_id":   "sessions are still addressed by their secret-keyed hash until they publish a snowflake ID",
	"passkey_id":   "passkeys are still addressed by their credential hash until they publish a snowflake ID",
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
