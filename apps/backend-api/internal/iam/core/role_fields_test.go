package core

import (
	"reflect"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/huma/fieldrules"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleFieldsSingleSourceAndProfiles(t *testing.T) {
	for _, test := range []struct {
		model    reflect.Type
		max      int
		optional bool
	}{
		{reflect.TypeFor[roleFields](), 4096, false},
		{reflect.TypeFor[platformRoleFields](), 1024, false},
		{reflect.TypeFor[rolePatchFields](), 4096, true},
	} {
		registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
		schema := huma.SchemaFromType(registry, test.model)
		assert.Equal(t, 128, *schema.Properties["name"].MaxLength)
		assert.Equal(t, test.max, *schema.Properties["description"].MaxLength)
		assert.Contains(t, schema.Properties["name"].Extensions, "x-field-rules")
		if test.optional {
			assert.Empty(t, schema.Required)
		} else {
			assert.Contains(t, schema.Required, "name")
		}
	}
	fields, err := normalizeRoleFields("  "+strings.Repeat("角", 42)+"ab  ", " untouched ", true)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("角", 42)+"ab", fields.Name)
	assert.Equal(t, " untouched ", fields.Description)
	_, err = normalizeRoleFields(strings.Repeat("角", 43), "", true)
	require.ErrorIs(t, err, ErrInvalidArgument)
	var issue *fieldrules.StringIssue
	require.ErrorAs(t, err, &issue)
	assert.Equal(t, "name", issue.Field)
}

func TestRoleDescriptionPatchDoesNotNormalizeUntouchedStoredName(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	role, err := harness.repository.CreateRole(t.Context(), 1, " imported role ", "before")
	require.NoError(t, err)
	description := "after"
	updated, err := harness.module.service.UpdateRole(t.Context(), 1, role.ID, nil, &description)
	require.NoError(t, err)
	assert.Equal(t, " imported role ", updated.Name)
	assert.Equal(t, "after", updated.Description)
}

func TestRoleByteErrorsUseStructuredThreeLanguageResponses(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		for _, test := range []struct {
			name  string
			code  string
			limit string
		}{
			{strings.Repeat("角", 43), "expected_max_bytes", "128"},
			{" \t ", "expected_min_bytes", "1"},
		} {
			response := harness.api.Post("/tenants/1/roles", "Authorization: Bearer test", "Accept-Language: "+locale, map[string]any{"name": test.name})
			require.Equal(t, 422, response.Code, response.Body.String())
			envelope := decodeEnvelope[respx.ValidationErrorData](t, response)
			require.Len(t, envelope.Data.ValidationErrors, 1)
			issue := envelope.Data.ValidationErrors[0]
			assert.Equal(t, "/name", issue.Path)
			assert.Equal(t, test.code, issue.Code)
			assert.Contains(t, issue.MessageKey, test.code)
			assert.Contains(t, issue.Message, test.limit)
			assert.NotContains(t, issue.Message, "respx.")
			assert.NotContains(t, response.Body.String(), test.name)
		}
	}
}

func TestRoleInternalServiceKeepsItsNormalizedInputProfile(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	raw := " " + strings.Repeat("a", 128) + " "
	role, err := harness.module.service.CreateRole(t.Context(), 1, raw, "")
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("a", 128), role.Name)
	response := harness.api.Post("/tenants/1/roles", "Authorization: Bearer test", map[string]any{"name": raw})
	assert.Equal(t, 422, response.Code, "HTTP must retain its additional raw character bound")
}
