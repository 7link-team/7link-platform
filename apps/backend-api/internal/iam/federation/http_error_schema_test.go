package federation

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installedErrorRef returns the error component reference huma.Register derives
// from the installed huma.NewError, discovered from a throwaway operation.
func installedErrorRef(t *testing.T, api huma.API) string {
	t.Helper()
	huma.Register(api, huma.Operation{
		OperationID: "error-schema-probe", Method: http.MethodGet, Path: "/error-schema-probe",
	}, func(context.Context, *struct{}) (*struct{ Body struct{ Value string } }, error) {
		return nil, nil
	})
	response, ok := api.OpenAPI().Paths["/error-schema-probe"].Get.Responses["default"]
	require.True(t, ok, "huma must document a default error response")
	media, ok := response.Content["application/json"]
	require.True(t, ok)
	require.NotEmpty(t, media.Schema.Ref)
	return media.Schema.Ref
}

// TestManualResponsesShareTheInstalledErrorSchema covers the browser and SAML
// routes, whose responses are written by hand instead of by huma.Register. They
// must reference the same error component as every generated response: the app
// replaces huma.NewError with its own envelope, so a hardcoded huma.ErrorModel
// would publish a schema the service never returns, and an inline schema would
// document the same body twice under different shapes.
func TestManualResponsesShareTheInstalledErrorSchema(t *testing.T) {
	respx.Install()
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("federation error schema test", "1.0.0"))
	expectedRef := installedErrorRef(t, api)

	manual := map[string]map[string]*huma.Response{
		"browserResponses": browserResponses(api.OpenAPI().Components.Schemas),
		"samlProtocolResponses": samlProtocolResponses(api, map[string]*huma.MediaType{
			"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
		}, true),
	}

	var checked int
	for source, responses := range manual {
		for status, response := range responses {
			media, ok := response.Content["application/json"]
			if !ok || status == "200" {
				continue // redirects carry no body; 200 is the success payload
			}
			assert.Equal(t, expectedRef, media.Schema.Ref,
				"%s %s must reference the installed error envelope", source, status)
			assert.Empty(t, media.Schema.Type,
				"%s %s must be a pure $ref, not an inline schema", source, status)
			checked++
		}
	}
	assert.Equal(t, 11, checked, "every documented federation error response must be covered")
	for source, responses := range manual {
		response, ok := responses["503"]
		require.True(t, ok, "%s must document an unavailable replay store", source)
		require.Contains(t, response.Content, "application/json")
		assert.Equal(t, expectedRef, response.Content["application/json"].Schema.Ref)
	}
}

// TestErrorResponseSchemaFollowsNewError proves the schema is derived from the
// installed constructor rather than pinned to one concrete type.
func TestErrorResponseSchemaFollowsNewError(t *testing.T) {
	previous := huma.NewError
	t.Cleanup(func() { huma.NewError = previous })

	huma.NewError = func(status int, message string, _ ...error) huma.StatusError {
		return &probeStatusError{Status: status, Detail: message}
	}
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	schema := errorResponseSchema(registry)
	assert.Equal(t, "#/components/schemas/ProbeStatusError", schema.Ref)
	assert.Contains(t, registry.Map(), "ProbeStatusError")
}

type probeStatusError struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func (e *probeStatusError) Error() string  { return e.Detail }
func (e *probeStatusError) GetStatus() int { return e.Status }
