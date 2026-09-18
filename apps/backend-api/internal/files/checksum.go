package files

import (
	"crypto/md5"  // Required interoperability checksum, not an authentication primitive.
	"crypto/sha1" // Required interoperability checksum, not an authentication primitive.
	"crypto/sha256"
	"crypto/sha512"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"math"
	"net/http"
	"strings"
)

var ErrCheckpoint = errors.New("files: invalid checksum checkpoint")
var ErrDigestHeaders = errors.New("files: four valid whole-file digest headers are required")

// Digests contains whole-file hexadecimal digests, never multipart ETags.
type Digests struct {
	MD5    string `json:"md5"`
	SHA1   string `json:"sha1"`
	SHA256 string `json:"sha256"`
	SHA512 string `json:"sha512"`
}

// ParseDigestHeaders requires the application's four whole-file checksum headers.
func ParseDigestHeaders(headers http.Header) (Digests, error) {
	var out Digests
	fields := []struct {
		name   string
		length int
		value  *string
	}{
		{"X-File-MD5", 32, &out.MD5}, {"X-File-SHA1", 40, &out.SHA1},
		{"X-File-SHA256", 64, &out.SHA256}, {"X-File-SHA512", 128, &out.SHA512},
	}
	for _, field := range fields {
		values := headers.Values(field.name)
		if len(values) != 1 {
			return Digests{}, ErrDigestHeaders
		}
		value := strings.TrimSpace(values[0])
		if len(value) != field.length {
			return Digests{}, ErrDigestHeaders
		}
		if _, err := hex.DecodeString(value); err != nil {
			return Digests{}, ErrDigestHeaders
		}
		*field.value = strings.ToLower(value)
	}
	return out, nil
}

// Checksums computes all four full-stream digests with bounded state.
type Checksums struct {
	offset int64
	hashes [4]hash.Hash
}

func NewChecksums() *Checksums {
	return &Checksums{hashes: [4]hash.Hash{md5.New(), sha1.New(), sha256.New(), sha512.New()}}
}

func (c *Checksums) Write(p []byte) (int, error) {
	if int64(len(p)) > math.MaxInt64-c.offset {
		return 0, ErrLength
	}
	for _, h := range c.hashes {
		_, _ = h.Write(p)
	}
	c.offset += int64(len(p))
	return len(p), nil
}

func (c *Checksums) Offset() int64 { return c.offset }

func (c *Checksums) Digests() Digests {
	return Digests{hex.EncodeToString(c.hashes[0].Sum(nil)), hex.EncodeToString(c.hashes[1].Sum(nil)), hex.EncodeToString(c.hashes[2].Sum(nil)), hex.EncodeToString(c.hashes[3].Sum(nil))}
}

type checksumState struct {
	Version int       `json:"version"`
	Offset  int64     `json:"offset"`
	States  [4][]byte `json:"states"`
}

type checksumEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

// Checkpoint binds the offset to every cumulative state in one opaque record.
// Integrity detects corruption; this record must remain server-owned.
func (c *Checksums) Checkpoint() ([]byte, error) {
	state := checksumState{Version: 1, Offset: c.offset}
	for i, h := range c.hashes {
		value, err := h.(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return nil, err
		}
		state.States[i] = value
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	return json.Marshal(checksumEnvelope{payload, hex.EncodeToString(sum[:])})
}

// RestoreChecksums rejects corrupt/version-incompatible state and stale offsets.
func RestoreChecksums(raw []byte, committedOffset int64) (*Checksums, error) {
	if len(raw) == 0 || len(raw) > 8192 || committedOffset < 0 {
		return nil, ErrCheckpoint
	}
	var envelope checksumEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrCheckpoint
	}
	sum := sha256.Sum256(envelope.Payload)
	if envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return nil, ErrCheckpoint
	}
	var state checksumState
	if json.Unmarshal(envelope.Payload, &state) != nil || state.Version != 1 || state.Offset != committedOffset {
		return nil, ErrCheckpoint
	}
	c := NewChecksums()
	for i, h := range c.hashes {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state.States[i]); err != nil {
			return nil, ErrCheckpoint
		}
	}
	c.offset = committedOffset
	return c, nil
}
