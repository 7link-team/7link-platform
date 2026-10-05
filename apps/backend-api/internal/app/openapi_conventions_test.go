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
		assert.Equal(t, []string{want}, success, "%s is a create operation", entry)
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
		query := map[string]*huma.Param{}
		for _, parameter := range entry.operation.Parameters {
			if parameter.In == "query" {
				query[parameter.Name] = parameter
			}
		}
		assert.NotContains(t, query, "q", "%s names its free-text filter q; use search", entry)
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
				assert.InDelta(t, 200, *limit.Schema.Maximum, 0, "%s: limit maximum", entry)
			}
		}
		offset := query["offset"]
		if assert.NotNil(t, offset, "%s pages by limit without offset", entry) && assert.NotNil(t, offset.Schema) {
			assert.EqualValues(t, 0, offset.Schema.Default, "%s: offset default", entry)
		}
	}
	assert.Greater(t, paged, 50, "the walk must reach the paged collections")
}
