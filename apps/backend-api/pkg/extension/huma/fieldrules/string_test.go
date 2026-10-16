package fieldrules

import (
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stringFixture struct {
	Name        string `json:"name" minLength:"1" maxLength:"8" normalize:"trim-space" minBytes:"1" maxBytes:"8"`
	Description string `json:"description,omitempty" maxBytes:"12"`
	Other       int    `json:"other"`
}

func TestStringRulesNormalizationAndByteBoundaries(t *testing.T) {
	rules, err := CompileStrings(reflect.TypeFor[stringFixture]())
	require.NoError(t, err)
	for _, test := range []struct {
		input    string
		ok       bool
		expected string
	}{
		{"  角色ab  ", true, "角色ab"}, {"角色abc", false, ""},
		{"\u0085角色\u3000", true, "角色"}, {" \t\n", false, ""},
		{"\ufeff", true, "\ufeff"},
	} {
		input := stringFixture{Name: test.input}
		original := input
		err := rules.Normalize(&input)
		if test.ok {
			require.NoError(t, err)
			assert.Equal(t, test.expected, input.Name)
		} else {
			var issue *StringIssue
			require.ErrorAs(t, err, &issue)
			assert.Equal(t, "name", issue.Field)
			assert.Equal(t, original, input, "failed validation must not partially normalize")
			assert.NotContains(t, issue.Error(), test.input)
		}
	}
	input := stringFixture{Name: "  ok  ", Description: strings.Repeat("描", 5)}
	err = rules.Normalize(&input)
	var issue *StringIssue
	require.ErrorAs(t, err, &issue)
	assert.Equal(t, "description", issue.Field)
	assert.Equal(t, "maxBytes", issue.Rule)
	assert.Equal(t, "  ok  ", input.Name)
	assert.NoError(t, rules.Check(stringFixture{Name: "  ok  "}))
	require.NoError(t, rules.Check(stringFixture{Name: "ok", Description: strings.Repeat("描", 4)}))
	assert.Error(t, rules.Normalize(nil))
	assert.Error(t, rules.Normalize(stringFixture{}))
	assert.Error(t, rules.Normalize(new(string)))
	assert.Error(t, rules.Check(nil))
	assert.Error(t, rules.Check(""))
	var empty *stringFixture
	assert.Error(t, rules.Normalize(empty))
}

func TestStringRulesRejectInvalidDeclarations(t *testing.T) {
	_, err := CompileStrings(nil)
	require.Error(t, err)
	_, err = CompileStrings(reflect.TypeFor[string]())
	require.Error(t, err)
	for _, tag := range []string{
		`json:"name" minBytes:"-1"`, `json:"name" maxBytes:"bad"`,
		`json:"name" minBytes:"9" maxBytes:"2"`, `json:"name" normalize:"unknown"`,
		`minBytes:"1"`, `json:"-" minBytes:"1"`, `json:"name" minBytes:""`,
	} {
		model := reflect.StructOf([]reflect.StructField{{Name: "Name", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(tag)}})
		_, err := CompileStrings(model)
		assert.Error(t, err, tag)
	}
	_, err = CompileStrings(reflect.TypeOf(struct {
		Number int `json:"number" maxBytes:"3"`
	}{}))
	assert.Error(t, err)
}

func TestStringRulesSchemaProjectionIsDerivedAndDoesNotChangeNullability(t *testing.T) {
	rules, err := CompileStrings(reflect.TypeFor[stringFixture]())
	require.NoError(t, err)
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	maximum := 3
	schema := &huma.Schema{Properties: map[string]*huma.Schema{
		"name": {Type: "string", Nullable: true}, "description": {Type: "string", MaxLength: &maximum},
	}}
	rules.Project(registry, schema)
	assert.True(t, schema.Properties["name"].Nullable)
	assert.Equal(t, 8, *schema.Properties["name"].MaxLength)
	assert.Equal(t, 3, *schema.Properties["description"].MaxLength)
	assert.Equal(t, []map[string]any{{"rule": "utf8-bytes", "args": map[string]any{"min": 1, "max": 8, "normalize": "trim-space"}}}, schema.Properties["name"].Extensions["x-field-rules"])
	assert.NotPanics(t, func() { rules.Project(registry, &huma.Schema{Properties: map[string]*huma.Schema{}}) })
	// Huma raw rules are separate: normalization does not impose raw character limits.
	input := stringFixture{Name: "          ok          "}
	require.NoError(t, rules.Normalize(&input))
	assert.Equal(t, "ok", input.Name)
}
