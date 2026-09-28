// Package jwsx parses and verifies compact-serialized JSON Web Signatures.
//
// It is the one implementation of JWS signature checking shared by every
// token verifier, so the trust decision is made and reviewed in one place.
// Callers state which algorithms they accept; the set can only narrow what
// this package implements, never widen it, so "none", the HMAC family and
// any other unimplemented identifier are refused whatever a caller allows.
package jwsx

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

// Algorithm identifiers this package can verify (RFC 7518, RFC 8037).
const (
	RS256 = "RS256"
	RS512 = "RS512"
	ES256 = "ES256"
	ES512 = "ES512"
	EdDSA = "EdDSA"
)

var (
	// ErrMalformed reports a value that is not a compact JWS.
	ErrMalformed = errors.New("malformed JWS")
	// ErrUnsupportedAlgorithm reports an algorithm the caller does not accept
	// or this package does not implement.
	ErrUnsupportedAlgorithm = errors.New("unsupported JWS algorithm")
	// ErrKeyType reports a key whose type does not match the algorithm.
	ErrKeyType = errors.New("JWS key type does not match algorithm")
	// ErrSignature reports a signature that does not verify.
	ErrSignature = errors.New("JWS signature invalid")
)

// Header holds the protected header members the verifiers read.
type Header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Token is a compact JWS split into its parts. Parse checks form only:
// nothing in a Token is trustworthy until Algorithms.Verify accepts it.
type Token struct {
	Header       Header
	SigningInput []byte
	Payload      []byte
	Signature    []byte
}

// Parse splits a compact JWS and decodes its header, payload and signature.
func Parse(raw string) (Token, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Token{}, fmt.Errorf("%w: expected three segments", ErrMalformed)
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Token{}, fmt.Errorf("%w: header encoding", ErrMalformed)
	}
	var token Token
	if err := json.Unmarshal(header, &token.Header); err != nil {
		return Token{}, fmt.Errorf("%w: header: %w", ErrMalformed, err)
	}
	if token.Payload, err = base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		return Token{}, fmt.Errorf("%w: payload encoding", ErrMalformed)
	}
	if token.Signature, err = base64.RawURLEncoding.DecodeString(parts[2]); err != nil {
		return Token{}, fmt.Errorf("%w: signature encoding", ErrMalformed)
	}
	token.SigningInput = []byte(parts[0] + "." + parts[1])
	return token, nil
}

// Algorithms is the set of algorithms a verifier accepts.
type Algorithms []string

// Allows reports whether alg is both accepted by this set and implemented.
// A caller can check it before fetching a key, so a refused algorithm never
// costs a key lookup.
func (a Algorithms) Allows(alg string) bool {
	return slices.Contains(a, alg) && implemented(alg)
}

func implemented(alg string) bool {
	switch alg {
	case RS256, RS512, ES256, ES512, EdDSA:
		return true
	default:
		return false
	}
}

// Verify checks signature over signingInput with key under alg, refusing any
// algorithm outside the set.
func (a Algorithms) Verify(alg string, key crypto.PublicKey, signingInput, signature []byte) error {
	if !a.Allows(alg) {
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, alg)
	}
	switch alg {
	case RS256, RS512:
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: RSA key required for %s", ErrKeyType, alg)
		}
		hash, digest := crypto.SHA256, sum256(signingInput)
		if alg == RS512 {
			hash, digest = crypto.SHA512, sum512(signingInput)
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, hash, digest, signature); err != nil {
			return fmt.Errorf("%w: %s", ErrSignature, alg)
		}
		return nil
	case ES256, ES512:
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: EC key required for %s", ErrKeyType, alg)
		}
		size := (ecKey.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return fmt.Errorf("%w: malformed ECDSA signature", ErrSignature)
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		digest := sum256(signingInput)
		if alg == ES512 {
			digest = sum512(signingInput)
		}
		if !ecdsa.Verify(ecKey, digest, r, s) {
			return fmt.Errorf("%w: %s", ErrSignature, alg)
		}
		return nil
	case EdDSA:
		edKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: Ed25519 key required for %s", ErrKeyType, alg)
		}
		if !ed25519.Verify(edKey, signingInput, signature) {
			return fmt.Errorf("%w: %s", ErrSignature, alg)
		}
		return nil
	default:
		// Unreachable while Allows and this switch list the same algorithms;
		// kept so an algorithm added to one but not the other fails closed.
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, alg)
	}
}

func sum256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func sum512(data []byte) []byte {
	sum := sha512.Sum512(data)
	return sum[:]
}
