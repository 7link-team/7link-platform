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
