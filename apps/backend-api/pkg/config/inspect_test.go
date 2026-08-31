package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectUsesMapstructureShapeAndRedactsSecrets(t *testing.T) {
	type nested struct {
		Enabled bool `mapstructure:"enabled"`
	}
	type sample struct {
		Name     string            `mapstructure:"name"`
		DSN      string            `mapstructure:"dsn" secret:"true"`
		Empty    string            `mapstructure:"empty" secret:"true"`
		Headers  map[string]string `mapstructure:"headers" secret:"true"`
		Timeout  time.Duration     `mapstructure:"timeout"`
		Nested   nested            `mapstructure:"nested"`
		Hidden   string            `mapstructure:"hidden" hidden:"true"`
		Legacy   string            `mapstructure:"legacy" deprecated:"use name"`
		Internal string            `mapstructure:"-"`
	}

	actual, err := Inspect(sample{
		Name:    "api",
		DSN:     "postgres://user:password@example/database",
		Headers: map[string]string{"authorization": "secret", "empty": ""},
		Timeout: 10 * time.Second,
		Nested:  nested{Enabled: true},
		Hidden:  "hidden",
		Legacy:  "legacy",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"name":    "api",
		"dsn":     RedactedValue,
		"empty":   "",
		"headers": map[string]any{"authorization": RedactedValue, "empty": ""},
		"timeout": "10s",
		"nested":  map[string]any{"enabled": true},
	}, actual)
}

func TestInspectRejectsInvalidValuesAndUnsupportedFields(t *testing.T) {
	_, err := Inspect(nil)
	assert.EqualError(t, err, "config: inspect requires a non-nil struct")
	_, err = Inspect("invalid")
	assert.EqualError(t, err, "config: inspect requires a struct")

	type unsupported struct {
		Channel chan int `mapstructure:"channel"`
	}
	_, err = Inspect(unsupported{})
	assert.EqualError(t, err, "config: inspect channel: unsupported field type chan")
}

func TestInspectSupportsPointersCollectionsAndScalarKinds(t *testing.T) {
	type nested struct {
		Value string `mapstructure:"value"`
	}
	text := "value"
	type sample struct {
		Pointer   *string           `mapstructure:"pointer"`
		Nil       *string           `mapstructure:"nil"`
		Interface any               `mapstructure:"interface"`
		Map       map[int]nested    `mapstructure:"map"`
		NilMap    map[string]string `mapstructure:"nil_map"`
		Slice     []uint            `mapstructure:"slice"`
		NilSlice  []string          `mapstructure:"nil_slice"`
		Array     [2]float64        `mapstructure:"array"`
		Signed    int8              `mapstructure:"signed"`
		Unsigned  uint16            `mapstructure:"unsigned"`
		Float     float32           `mapstructure:"float"`
		SecretPtr *string           `mapstructure:"secret_ptr" secret:"true"`
		SecretMap map[string]string `mapstructure:"secret_map" secret:"true"`
		SecretSeq []string          `mapstructure:"secret_seq" secret:"true"`
		SecretNum int               `mapstructure:"secret_num" secret:"true"`
		SecretNil *string           `mapstructure:"secret_nil" secret:"true"`
	}

	actual, err := Inspect(&sample{
		Pointer: &text, Interface: nested{Value: "nested"}, Map: map[int]nested{2: {Value: "two"}},
		Slice: []uint{1, 2}, Array: [2]float64{1.5, 2.5}, Signed: -2, Unsigned: 3, Float: 4.5,
		SecretPtr: &text, SecretMap: map[string]string{"token": "secret", "empty": ""},
		SecretSeq: []string{"secret", ""}, SecretNum: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, "value", actual["pointer"])
	assert.Nil(t, actual["nil"])
	assert.Equal(t, map[string]any{"value": "nested"}, actual["interface"])
	assert.Equal(t, map[string]any{"2": map[string]any{"value": "two"}}, actual["map"])
	assert.Nil(t, actual["nil_map"])
	assert.Equal(t, []any{uint64(1), uint64(2)}, actual["slice"])
	assert.Nil(t, actual["nil_slice"])
	assert.Equal(t, []any{1.5, 2.5}, actual["array"])
	assert.Equal(t, int64(-2), actual["signed"])
	assert.Equal(t, uint64(3), actual["unsigned"])
	assert.InDelta(t, 4.5, actual["float"], 0.001)
	assert.Equal(t, RedactedValue, actual["secret_ptr"])
	assert.Equal(t, map[string]any{"token": RedactedValue, "empty": ""}, actual["secret_map"])
	assert.Equal(t, []any{RedactedValue, ""}, actual["secret_seq"])
	assert.Equal(t, RedactedValue, actual["secret_num"])
	assert.Nil(t, actual["secret_nil"])
}

func TestInspectPropagatesUnsupportedCollectionValues(t *testing.T) {
	type invalidMap struct {
		Values map[string]chan int `mapstructure:"values"`
	}
	_, err := Inspect(invalidMap{Values: map[string]chan int{"bad": make(chan int)}})
	assert.ErrorContains(t, err, "unsupported field type chan")

	type invalidSlice struct {
		Values []chan int `mapstructure:"values"`
	}
	_, err = Inspect(invalidSlice{Values: []chan int{make(chan int)}})
	assert.ErrorContains(t, err, "unsupported field type chan")
}
