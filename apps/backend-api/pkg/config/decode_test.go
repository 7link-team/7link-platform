package config

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldTagDecodeHookHandlesPointersAndNonMaps(t *testing.T) {
	type sample struct {
		Name string `mapstructure:"name"`
	}
	target := reflect.TypeOf((**sample)(nil)).Elem()
	data := "not-a-map"
	got, err := fieldTagDecodeHook(nil, target, data)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestTrimRawValueHandlesSlicesAndOtherValues(t *testing.T) {
	assert.Equal(t, []string{"first", "second"}, trimRawValue([]string{" first ", "second "}))
	assert.Equal(t, []any{"first", []string{"second"}, 3}, trimRawValue([]any{
		" first ", []string{" second "}, 3,
	}))
	assert.Equal(t, 42, trimRawValue(42))
	assert.True(t, isEmptyRawValue(nil))
	assert.False(t, isEmptyRawValue(42))
}

func TestFieldTagDecodeHookAppliesSquashedDefaultsAndTrim(t *testing.T) {
	type Embedded struct {
		Name    string `mapstructure:"name" trim:"true"`
		Timeout string `mapstructure:"timeout" trim:"true" default:" 5s "`
	}
	type sample struct {
		Embedded `mapstructure:",squash"`
	}

	values := map[string]any{"name": " api "}
	got, err := fieldTagDecodeHook(nil, reflect.TypeOf(sample{}), values)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "api", "timeout": "5s"}, got)
}
