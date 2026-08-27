package config

import (
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
