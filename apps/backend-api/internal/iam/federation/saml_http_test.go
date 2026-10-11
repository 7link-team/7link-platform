package federation

import (
	"testing"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSAMLProtocolRedirectDeclaresAHeaderObject keeps the single sign-on
// redirect valid OpenAPI: its Location is a Header Object, named by its map key,
// so name and in are forbidden. Only the redirecting endpoint documents it.
func TestSAMLProtocolRedirectDeclaresAHeaderObject(t *testing.T) {
	respx.Install()
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("saml redirect header test", "1.0.0"))
	success := map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}}}

	redirecting := samlProtocolResponses(api, success, true)
	require.Contains(t, redirecting, "302")
	location := redirecting["302"].Headers["Location"]
	require.NotNil(t, location)
	assert.True(t, location.Required)
	assert.Empty(t, location.Name, "a header object has no name")
	assert.Empty(t, location.In, "a header object has no in")

	assert.NotContains(t, samlProtocolResponses(api, success, false), "302")
}
