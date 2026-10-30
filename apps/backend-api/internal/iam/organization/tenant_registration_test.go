package organization

import (
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantRegistrationAcceptsActiveOwnerWithoutVerifiedMailbox(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	s, bootstrap, owners := newTenantServiceForTest(t, h.organizationHarness, newOrganizationTestIDs(1000))
	s.registrationEnabled = true
	owners.principals[1] = identity.Principal{ID: 1, LoginName: "new-account", DisplayName: "New account", Status: "active"}
	_, api := humatest.New(t)
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), organizationHTTPTokenVerifier{}, organizationPermissionChecker{}, organizationHTTPMembershipChecker{})
	registerTenantRegistrationHTTP(api, s, registrar)
	result := api.Post("/tenant-registrations", "Authorization: Bearer test", map[string]any{"slug": "new-workspace", "name": "New workspace"})
	require.Equal(t, http.StatusCreated, result.Code, result.Body.String())
	require.Len(t, bootstrap.inputs, 1)
	assert.EqualValues(t, 1, bootstrap.inputs[0].OwnerID)
	assert.False(t, owners.principals[1].EmailVerified)
	s.registrationEnabled = false
	result = api.Post("/tenant-registrations", "Authorization: Bearer test", map[string]any{"slug": "disabled-workspace", "name": "Disabled"})
	assert.Equal(t, http.StatusForbidden, result.Code)
}

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
