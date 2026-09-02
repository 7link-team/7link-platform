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
	assert.Equal(t, "string", ID(1).Schema(nil).Type)
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

// TestIDZeroRoundTripsThroughJSON guards the defect this type shipped with:
// MarshalJSON emitted "0" for the zero value, which Parse then refused because
// an ID must be positive. Any column holding an optional reference — an actor
// no authentication middleware has supplied, an unset parent — produced JSON
// the type could not read back.
func TestIDZeroRoundTripsThroughJSON(t *testing.T) {
	encoded, err := json.Marshal(ID(0))
	require.NoError(t, err)
	assert.JSONEq(t, "null", string(encoded), "the zero ID has no decimal form")

	var decoded ID
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.True(t, decoded.Zero())
}

func TestIDRoundTripsPreservingPrecision(t *testing.T) {
	// A snowflake exceeds 2^53, so it must never cross JSON as a number.
	const snowflake = ID(2366488506482438144)
	encoded, err := json.Marshal(snowflake)
	require.NoError(t, err)
	assert.Equal(t, `"2366488506482438144"`, string(encoded))

	var decoded ID
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, snowflake, decoded)
}

func TestIDSchemaIsNullableString(t *testing.T) {
	schema := ID(0).Schema(nil)
	assert.Equal(t, "string", schema.Type, "numbers would lose snowflake precision in JavaScript")
	assert.True(t, schema.Nullable, "the zero ID marshals as null, so the schema must allow it")
}

// TestIDInStructOmitsNothingWhenZero documents what a caller sees: a zero
// reference is present and null, not absent, so "unknown" stays distinguishable
// from "not requested".
func TestIDInStructOmitsNothingWhenZero(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Actor ID `json:"actor"`
	}{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"actor":null}`, string(encoded))
}

// TestZeroIDRoundTripsThroughText covers the text paths — map keys, XML, form
// encoding — which use MarshalText rather than MarshalJSON. Rendering the zero
// as "0" would emit a value Parse refuses to read back.
func TestZeroIDRoundTripsThroughText(t *testing.T) {
	encoded, err := ID(0).MarshalText()
	require.NoError(t, err)
	assert.Empty(t, string(encoded))

	var decoded ID
	require.NoError(t, decoded.UnmarshalText(encoded))
	assert.True(t, decoded.Zero())

	encoded, err = ID(42).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "42", string(encoded))
	require.NoError(t, decoded.UnmarshalText(encoded))
	assert.Equal(t, ID(42), decoded)
}
