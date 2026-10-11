package organization

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTenantRegistrationPolicyPublishesANamedSchema keeps the availability
// answer a named component. An anonymous struct would be published under its Go
// type literal, a component name OpenAPI forbids and generators cannot address.
func TestTenantRegistrationPolicyPublishesANamedSchema(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	operation := h.api.OpenAPI().Paths["/tenant-registrations/options"].Get
	require.NotNil(t, operation)
	media := operation.Responses["200"].Content["application/json"]
	require.NotNil(t, media)
	schemas := h.api.OpenAPI().Components.Schemas
	envelope := schemas.SchemaFromRef(media.Schema.Ref)
	require.NotNil(t, envelope, "the success body is a component reference")
	data := envelope.Properties["data"]
	require.NotNil(t, data)
	assert.Equal(t, "#/components/schemas/TenantRegistrationPolicy", data.Ref)
	for name := range schemas.Map() {
		assert.Regexp(t, `^[a-zA-Z0-9.\-_]+$`, name, "a component name OpenAPI forbids")
	}

	options := h.api.Get("/tenant-registrations/options", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, options.Code)
	assert.Contains(t, options.Body.String(), `"data":{"enabled":false}`)
}
