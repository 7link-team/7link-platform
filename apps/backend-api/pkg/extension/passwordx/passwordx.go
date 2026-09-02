// Package passwordx hashes and verifies passwords using bounded Argon2id parameters.
package passwordx

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memory      = 19 * 1024
	iterations  = 2
	parallelism = 1
	saltLength  = 16
	keyLength   = 32
)

var ErrInvalidHash = errors.New("invalid password hash")

func Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is empty")
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrInvalidHash
	}
	var m uint64
	var t uint64
	var p uint64
	for _, value := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(value, "=", 2)
		if len(pair) != 2 {
			return false, ErrInvalidHash
		}
		parsed, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false, ErrInvalidHash
		}
		switch pair[0] {
		case "m":
			m = parsed
		case "t":
			t = parsed
		case "p":
			p = parsed
		default:
			return false, ErrInvalidHash
		}
	}
	// This service has only ever generated this parameter set. Accepting
	// attacker-amplified PHC costs would turn a damaged hash row into an OOM or
	// CPU exhaustion primitive; a future cost upgrade must add an explicit,
	// similarly bounded compatibility set.
	if m != memory || t != iterations || p != parallelism {
		return false, ErrInvalidHash
	}
	if len(parts[4]) != base64.RawStdEncoding.EncodedLen(saltLength) ||
		len(parts[5]) != base64.RawStdEncoding.EncodedLen(keyLength) {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltLength {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != keyLength {
		return false, ErrInvalidHash
	}
	// The exact generated t/m/p and digest length were validated above.
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p), uint32(len(want))) //nolint:gosec // G115: values bounded by validation above
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
