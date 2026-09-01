package audit

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactJSONNestedFields(t *testing.T) {
	redactor := newRedactor(nil)
	out := requireRedactedJSON(t, redactor, []byte(`{
		"name": "alice",
		"password": "hunter2",
		"meta": {"access_token": "abc", "tags": ["x", "y"]},
		"list": [{"secret": "s1"}, {"plain": "keep"}]
	}`))
	require.NotContains(t, out, "hunter2")
	require.Contains(t, out, "[REDACTED]")
	require.Contains(t, out, "alice")
	require.Contains(t, out, "keep")
}

func TestRedactJSONCaseInsensitiveAndExtraKeys(t *testing.T) {
	redactor := newRedactor([]string{"phone", "Email"})
	out := requireRedactedJSON(t, redactor, []byte(`{"Password": "p", "PHONE": "555", "email": "a@b.c", "ok": 1}`))
	require.Contains(t, out, "[REDACTED]")
	require.NotContains(t, out, "555")
	require.NotContains(t, out, "a@b.c")
	require.Contains(t, out, "1")
}

func TestRedactJSONMatchesSeparatorVariants(t *testing.T) {
	// The built-in keys are snake_case, but JSON APIs commonly spell the same
	// fields in camelCase or kebab-case. Matching ignores word separators so a
	// default key covers every spelling of the field it names.
	redactor := newRedactor(nil)
	out := requireRedactedJSON(t, redactor, []byte(`{
		"accessToken": "at-leak",
		"refresh-token": "rt-leak",
		"clientSecret": "cs-leak",
		"cardNumber": "4111111111111111",
		"orderNumber": "keep-me"
	}`))
	require.NotContains(t, out, "at-leak")
	require.NotContains(t, out, "rt-leak")
	require.NotContains(t, out, "cs-leak")
	require.NotContains(t, out, "4111111111111111")
	require.Contains(t, out, "keep-me")
}

func TestRedactJSONConfiguredKeyMatchesOtherSpellings(t *testing.T) {
	// A configured key is normalized the same way, so operators do not have to
	// enumerate every spelling their API happens to use.
	redactor := newRedactor([]string{"phone_number"})
	out := requireRedactedJSON(t, redactor, []byte(`{"phoneNumber":"555","ok":1}`))
	require.NotContains(t, out, "555")
	require.Contains(t, out, `"ok":1`)
}

func TestRedactJSONMalformedReturnsNoBody(t *testing.T) {
	redactor := newRedactor(nil)
	out, err := redactor.RedactJSON([]byte(`{"password":"secret"`))
	assert.Error(t, err)
	assert.Empty(t, out)
}

func TestRedactJSONScalarAndEmpty(t *testing.T) {
	redactor := newRedactor(nil)
	assert.Equal(t, `"just text"`, requireRedactedJSON(t, redactor, []byte(`"just text"`)))
	assert.Equal(t, "", requireRedactedJSON(t, redactor, nil))
}

func TestRedactJSONPreservesNumberPrecision(t *testing.T) {
	// 19-digit snowflake IDs exceed float64 precision (2^53); a decode-to-any
	// round-trip corrupts them, so the redactor must keep them verbatim.
	redactor := newRedactor(nil)
	out := requireRedactedJSON(t, redactor, []byte(`{"order_id":2366488506482438144,"ok":1}`))
	require.Contains(t, out, `2366488506482438144`)
	require.Contains(t, out, `"ok":1`)
}

func TestRedactJSONPreservesObjectKeyOrder(t *testing.T) {
	redactor := newRedactor(nil)
	out := requireRedactedJSON(t, redactor, []byte(`{"a":1,"password":"x","b":2}`))
	//nolint:testifylint // Key order is the assertion here; JSONEq ignores it.
	assert.Equal(t, `{"a":1,"password":"[REDACTED]","b":2}`, out)
}

func TestRedactJSONNestedNumberPrecision(t *testing.T) {
	redactor := newRedactor([]string{"pin"})
	out := requireRedactedJSON(t, redactor, []byte(`{"list":[{"id":2366488506482438144,"pin":"1234"}]}`))
	require.Contains(t, out, `2366488506482438144`)
	require.Contains(t, out, `"pin":"[REDACTED]"`)
}

func TestRedactJSONRejectsTrailingGarbage(t *testing.T) {
	redactor := newRedactor(nil)
	out, err := redactor.RedactJSON([]byte(`{"a":1} extra`))
	assert.Error(t, err)
	assert.Empty(t, out)
}

func requireRedactedJSON(t *testing.T, redactor *redactor, raw []byte) string {
	t.Helper()
	out, err := redactor.RedactJSON(raw)
	require.NoError(t, err)
	return out
}

func TestTruncateStringRuneSafe(t *testing.T) {
	// "你好世界" is 12 bytes; cutting at 6 bytes must not split a rune.
	input := "你好世界"
	out := truncateString(input, 6)
	assert.True(t, strings.HasPrefix(input, out) || len(out) == 6)
	require.LessOrEqual(t, len(out), 6)
	// ASCII shorter than max is unchanged.
	assert.Equal(t, "short", truncateString("short", 100))
	// Empty input unchanged.
	assert.Equal(t, "", truncateString("", 10))
	// Non-positive max returns input unchanged.
	assert.Equal(t, "abc", truncateString("abc", 0))
}

func TestIsJSONContentType(t *testing.T) {
	for _, value := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/vnd.api+json",
		"Application/JSON",
	} {
		assert.True(t, isJSONContentType(value), value)
	}
	for _, value := range []string{
		"", "text/plain", "multipart/form-data", "application/x-www-form-urlencoded",
		"application/xml", "application/problem+xml",
	} {
		assert.False(t, isJSONContentType(value), value)
	}
}

func TestRedactValueMarker(t *testing.T) {
	assert.Equal(t, redactionMarker, "[REDACTED]")
}

func TestTruncateStringInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe, 0x00, 0x01})
	out := truncateString(invalid, 3)
	// No invalid bytes survive; the result is valid UTF-8.
	assert.True(t, utf8.ValidString(out))
	assert.LessOrEqual(t, len(out), 3)
}
