package config

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type requiredChild struct {
	Token string `mapstructure:"token" required:"true"`
}

type requiredSample struct {
	Name     string                   `mapstructure:"name" required:"true"`
	Child    requiredChild            `mapstructure:"child"`
	Tenants  map[string]requiredChild `mapstructure:"tenants"`
	Optional *requiredChild           `mapstructure:"optional"`
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		config  requiredSample
		wantErr string
	}{
		{name: "top level", config: requiredSample{}, wantErr: "config: name is required"},
		{
			name:    "nested field",
			config:  requiredSample{Name: "api"},
			wantErr: "config: child.token is required",
		},
		{
			name: "map entry",
			config: requiredSample{
				Name:    "api",
				Child:   requiredChild{Token: "configured"},
				Tenants: map[string]requiredChild{"acme": {}},
			},
			wantErr: "config: tenants.acme.token is required",
		},
		{
			name: "valid",
			config: requiredSample{
				Name:    "api",
				Child:   requiredChild{Token: "configured"},
				Tenants: map[string]requiredChild{"acme": {Token: "configured"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.config)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateDoesNotTrimWithoutTag(t *testing.T) {
	config := requiredSample{Name: " \t ", Child: requiredChild{Token: "configured"}}
	require.NoError(t, Validate(&config))
	assert.Equal(t, " \t ", config.Name)
}

func TestProcessAppliesTrimBeforeRequired(t *testing.T) {
	type trimmedConfig struct {
		Name string `mapstructure:"name" trim:"true" required:"true"`
	}

	config := trimmedConfig{Name: "  api  "}
	require.NoError(t, Process(&config))
	assert.Equal(t, "api", config.Name)

	config.Name = " \t "
	require.EqualError(t, Process(&config), "config: name is required")
	assert.Empty(t, config.Name)
}

func TestProcessTrimsNestedMapEntries(t *testing.T) {
	type child struct {
		Token string `mapstructure:"token" trim:"true" required:"true"`
	}
	type sample struct {
		Children map[string]child `mapstructure:"children"`
	}

	config := sample{Children: map[string]child{"acme": {Token: " token "}}}
	require.NoError(t, Process(&config))
	assert.Equal(t, "token", config.Children["acme"].Token)
}

func TestValidateRejectsInvalidInput(t *testing.T) {
	assert.Error(t, Validate(nil))
	assert.Error(t, Validate((*requiredSample)(nil)))
	assert.Error(t, Validate("not-a-struct"))
}

func TestNormalizeRejectsInvalidInputAndTrimType(t *testing.T) {
	assert.Error(t, Normalize(nil))
	assert.Error(t, Normalize(requiredSample{}))

	type invalid struct {
		Count int `mapstructure:"count" trim:"true"`
	}
	assert.EqualError(t, Normalize(&invalid{}), "config: count uses trim on unsupported type int")
}

func TestValidatePattern(t *testing.T) {
	type sample struct {
		Name string `mapstructure:"name" validate:"^[a-z]+$"`
	}

	require.NoError(t, Validate(sample{Name: "backend"}))
	assert.EqualError(
		t,
		Validate(sample{Name: "backend-api"}),
		"config: name does not match validation pattern",
	)
}

func TestValidateRejectsInvalidPatternAndType(t *testing.T) {
	type invalidPattern struct {
		Name string `mapstructure:"name" validate:"["`
	}
	err := Validate(invalidPattern{Name: "backend"})
	assert.ErrorContains(t, err, `config: name has invalid validation pattern "["`)

	type invalidType struct {
		Count int `mapstructure:"count" validate:"^[0-9]+$"`
	}
	assert.EqualError(
		t,
		Validate(invalidType{Count: 1}),
		"config: count validation requires a string, got int",
	)
}

func TestNormalizePropagatesNestedCollectionErrors(t *testing.T) {
	type invalidChild struct {
		Count int `mapstructure:"count" trim:"true"`
	}

	for _, value := range []any{
		&struct {
			Child invalidChild `mapstructure:"child"`
		}{},
		&struct {
			Children map[string]invalidChild `mapstructure:"children"`
		}{Children: map[string]invalidChild{"first": {}}},
		&struct {
			Children []invalidChild `mapstructure:"children"`
		}{Children: []invalidChild{{}}},
	} {
		assert.ErrorContains(t, Normalize(value), "uses trim on unsupported type int")
	}
}

func TestNormalizeAndValidateReflectionEdgeCases(t *testing.T) {
	assert.Error(t, Process(nil))
	value := 1
	assert.ErrorContains(t, Normalize(&value), "pointer to a struct")
	assert.NoError(t, trimValue(reflect.Value{}, "invalid"))
	assert.ErrorContains(t, trimValue(reflect.ValueOf(" value "), "name"), "cannot be trimmed")

	type pointerPattern struct {
		Name *string `mapstructure:"name" validate:"^[a-z]+$"`
	}
	name := "valid"
	require.NoError(t, Validate(pointerPattern{Name: &name}))
	assert.True(t, isMissing(reflect.Value{}))
}

func TestValidateTraversesSlicesInOrder(t *testing.T) {
	type child struct {
		Name string `mapstructure:"name" required:"true"`
	}
	type sample struct {
		Children []child `mapstructure:"children"`
	}

	err := Validate(sample{Children: []child{{Name: "first"}, {}}})
	assert.EqualError(t, err, "config: children.1.name is required")
}

func TestProcessFlattensSquashedNormalizationAndValidationPaths(t *testing.T) {
	type Embedded struct {
		Name string `mapstructure:"name" trim:"true" required:"true"`
	}
	type sample struct {
		Embedded `mapstructure:",squash"`
	}

	valid := sample{Embedded: Embedded{Name: " api "}}
	require.NoError(t, Process(&valid))
	assert.Equal(t, "api", valid.Name)

	missing := sample{Embedded: Embedded{Name: " "}}
	assert.EqualError(t, Process(&missing), "config: name is required")
}
