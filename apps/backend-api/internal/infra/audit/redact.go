package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// errUnexpectedToken guards against structurally impossible decoder states
// (e.g. a non-string object key) that would otherwise produce corrupt output.
var errUnexpectedToken = errors.New("audit: unexpected JSON token")

// defaultSensitiveKeys are JSON field names whose values are replaced with
// [REDACTED] when capturing request or response bodies. Keys are matched
// case-insensitively after trimming.
var defaultSensitiveKeys = map[string]struct{}{
	"password":          {},
	"passwd":            {},
	"pwd":               {},
	"secret":            {},
	"token":             {},
	"access_token":      {},
	"refresh_token":     {},
	"authorization":     {},
	"api_key":           {},
	"apikey":            {},
	"private_key":       {},
	"credit_card":       {},
	"card_no":           {},
	"card_number":       {},
	"cvv":               {},
	"id_card":           {},
	"idcard":            {},
	"ssn":               {},
	"client_secret":     {},
	"client_credential": {},
}

// redactor redacts JSON documents by replacing values under sensitive keys.
// The key set is immutable once built, so the redactor is safe for concurrent
// use.
type redactor struct {
	sensitive map[string]struct{}
}

func newRedactor(extra []string) *redactor {
	keys := make(map[string]struct{}, len(defaultSensitiveKeys)+len(extra))
	for key := range defaultSensitiveKeys {
		keys[key] = struct{}{}
	}
	for _, key := range extra {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			keys[key] = struct{}{}
		}
	}
	return &redactor{sensitive: keys}
}

// RedactJSON replaces values under sensitive keys with the redaction marker and
// returns the resulting document. The rewrite is token-streaming: numbers are
// preserved verbatim (so snowflake IDs and other integers above 2^53 are not
// corrupted by a float64 round-trip) and object key order is kept, unlike a
// decode-to-any re-encode. Malformed JSON returns an error and no body: falling
// back to the original input could persist an unredacted secret. The result is
// a valid UTF-8 string.
func (r *redactor) RedactJSON(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out bytes.Buffer
	if err := writeJSONValue(decoder, &out, r.sensitive); err != nil {
		return "", err
	}
	// Reject trailing garbage the way json.Unmarshal does, so a truncated or
	// multi-document body is not silently accepted as redacted output.
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errUnexpectedToken
		}
		return "", err
	}
	return sanitize(out.String()), nil
}

// writeJSONValue writes one complete JSON value from decoder to out, replacing
// values under sensitive keys with the redaction marker.
func writeJSONValue(decoder *json.Decoder, out *bytes.Buffer, sensitive map[string]struct{}) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return writeScalar(out, token)
	}
	switch delim {
	case '{':
		return writeJSONObject(decoder, out, sensitive)
	case '[':
		return writeJSONArray(decoder, out, sensitive)
	default:
		return errUnexpectedToken
	}
}

func writeJSONObject(decoder *json.Decoder, out *bytes.Buffer, sensitive map[string]struct{}) error {
	out.WriteByte('{')
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errUnexpectedToken
		}
		if err := writeScalar(out, key); err != nil {
			return err
		}
		out.WriteByte(':')
		if isSensitiveKey(key, sensitive) {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
			out.WriteString(`"[REDACTED]"`)
		} else if err := writeJSONValue(decoder, out, sensitive); err != nil {
			return err
		}
		if decoder.More() {
			out.WriteByte(',')
		}
	}
	if _, err := decoder.Token(); err != nil { // consume the closing '}'
		return err
	}
	out.WriteByte('}')
	return nil
}

func writeJSONArray(decoder *json.Decoder, out *bytes.Buffer, sensitive map[string]struct{}) error {
	out.WriteByte('[')
	for decoder.More() {
		if err := writeJSONValue(decoder, out, sensitive); err != nil {
			return err
		}
		if decoder.More() {
			out.WriteByte(',')
		}
	}
	if _, err := decoder.Token(); err != nil { // consume the closing ']'
		return err
	}
	out.WriteByte(']')
	return nil
}

// skipJSONValue consumes one complete JSON value from decoder without writing
// it, so a value under a sensitive key is discarded rather than captured.
func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil // scalar consumed
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token() // consume the closing delimiter
	return err
}

// writeScalar re-encodes a scalar token. json.Number is written verbatim, which
// preserves the precision of integers beyond 2^53.
func writeScalar(out *bytes.Buffer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	out.Write(encoded)
	return nil
}

func isSensitiveKey(key string, sensitive map[string]struct{}) bool {
	_, ok := sensitive[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// redactionMarker is the literal replacement for sensitive values.
const redactionMarker = "[REDACTED]"

// truncateString cuts s to at most max bytes. The cut is at a rune boundary so
// the result is always valid UTF-8: bytes that would split a multi-byte rune
// are dropped. The replacement character guards against any residual invalid
// sequences in the source body.
func truncateString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, string(utf8.RuneError))
	}
	trimmed := s[:max]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func sanitize(s string) string {
	return strings.ToValidUTF8(s, string(utf8.RuneError))
}

// isJSONContentType reports whether a Content-Type header represents a JSON
// body: application/json or any structured-suffix (+json) type.
func isJSONContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}
