package guid

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIDJSONAndText(t *testing.T) {
	encoded, err := json.Marshal(ID(123))
	require.NoError(t, err)
	assert.JSONEq(t, `"123"`, string(encoded))

	for _, input := range []string{`"456"`, `456`} {
		var id ID
		require.NoError(t, json.Unmarshal([]byte(input), &id))
		assert.Equal(t, ID(456), id)
	}
	var id ID
	require.NoError(t, id.UnmarshalText([]byte("789")))
	assert.Equal(t, "789", id.String())
	assert.Error(t, json.Unmarshal([]byte(`"invalid"`), &id))
}

func TestIDSQL(t *testing.T) {
	value, err := ID(7).Value()
	require.NoError(t, err)
	assert.Equal(t, int64(7), value)

	var id ID
	for _, test := range []struct {
		source   any
		expected ID
	}{
		{source: int64(11), expected: 11},
		{source: []byte("12"), expected: 12},
		{source: "13", expected: 13},
		{source: nil, expected: 0},
	} {
		require.NoError(t, id.Scan(test.source))
		assert.Equal(t, test.expected, id)
	}
	assert.Error(t, id.Scan(3.14))
	assert.Equal(t, "string", string(ID(1).Schema(nil).Type))
}

func TestParseIDRequiresPositiveDecimal(t *testing.T) {
	id, err := Parse(`"123"`)
	require.NoError(t, err)
	assert.Equal(t, ID(123), id)
	for _, input := range []string{"0", "-1", "invalid"} {
		_, err := Parse(input)
		assert.Error(t, err)
	}
}

func TestIDConvenienceMethodsAndEmptyValues(t *testing.T) {
	encoded, err := ID(42).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "42", string(encoded))
	assert.Equal(t, int64(42), ID(42).Int64())
	assert.True(t, ID(0).Zero())
	assert.False(t, ID(1).Zero())

	for _, input := range []string{"", "null", `""`, `"  "`} {
		id, err := Parse(input)
		require.NoError(t, err)
		assert.Zero(t, id)
	}

	var id ID
	assert.Error(t, id.Scan("invalid"))
}
