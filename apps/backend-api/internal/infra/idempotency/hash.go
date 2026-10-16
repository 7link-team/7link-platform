package idempotency

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
)

// HashRequest preserves typed field order and exact numbers while excluding only
// the top-level seen_seq retry cursor. Encoding failures are errors, never a
// shared zero hash which could replay an unrelated request.
func HashRequest(method, routeTemplate string, pathParams map[string]string, body any) ([32]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, err
	}
	canonical, err := withoutSeenSeq(encoded)
	if err != nil {
		return [32]byte{}, err
	}
	paths, err := json.Marshal(pathParams)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(method + "\n" + routeTemplate + "\n" + string(paths) + "\n" + string(canonical))), nil
}

func withoutSeenSeq(encoded []byte) ([]byte, error) {
	// A streaming object pass keeps struct field order and RawMessage numbers.
	// Arrays/scalars contain no top-level field to exclude.
	if len(encoded) == 0 || encoded[0] != '{' {
		return encoded, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	out := bytes.NewBufferString("{")
	first := true
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrInvalidKey
		}
		if key == "seen_seq" {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalidKey
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
