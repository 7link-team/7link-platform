package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignBindsTheTimestampToTheBody(t *testing.T) {
	secret := []byte("the-signing-secret")
	body := []byte(`{"event":{"id":"1"}}`)

	signature := sign(secret, 1700000000, body)

	// Verify the way a receiver does, so a change to the signed content shows up
	// here rather than at a customer's endpoint.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("1700000000"))
	mac.Write([]byte{'.'})
	mac.Write(body)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), signature)

	// The timestamp is inside the signed content, so replaying a body under a
	// different timestamp does not verify.
	assert.NotEqual(t, signature, sign(secret, 1700000001, body))
	assert.NotEqual(t, signature, sign(secret, 1700000000, append(body, ' ')))
	assert.NotEqual(t, signature, sign([]byte("another-secret"), 1700000000, body))
}

func TestSignIsStableAcrossCalls(t *testing.T) {
	// A receiver that stores a signature to detect duplicates needs the same
	// input to produce the same output.
	body := []byte("payload")
	first := sign([]byte("s"), 42, body)
	assert.Equal(t, first, sign([]byte("s"), 42, body))
	assert.Len(t, first, sha256.Size*2, "a hex SHA-256 digest")
}

func TestMatchesEventTypeTreatsAnEmptySelectionAsEverything(t *testing.T) {
	// A tenant that wants everything would otherwise have to keep a list in step
	// with every event type this platform ever adds.
	assert.True(t, matchesEventType("", "principal_created"))
	assert.True(t, matchesEventType("   ", "anything"))

	assert.True(t, matchesEventType("principal_created federation_login", "federation_login"))
	assert.False(t, matchesEventType("principal_created", "federation_login"))
	assert.False(t, matchesEventType("principal_created", "principal_created_extra"),
		"matching is by whole type, not by prefix")
}

func TestNormalizeEventTypesSortsAndDeduplicates(t *testing.T) {
	// Two subscriptions asking for the same thing should store the same value,
	// otherwise the stored list reads as meaningful ordering that it is not.
	types, err := normalizeEventTypes([]string{"role_granted", "principal_created", "role_granted", "  "})
	require.NoError(t, err)
	assert.Equal(t, "principal_created role_granted", types)

	empty, err := normalizeEventTypes(nil)
	require.NoError(t, err)
	assert.Empty(t, empty, "no selection means every type")
}

func TestNormalizeEventTypesRefusesWhatCouldNotBeMatched(t *testing.T) {
	// A type containing a space could never match: the stored list is
	// space-separated, so it would be read back as two types.
	_, err := normalizeEventTypes([]string{"has space"})
	require.Error(t, err)

	_, err = normalizeEventTypes([]string{strconv.Itoa(0) + string(make([]byte, 64))})
	require.Error(t, err, "a type longer than the audit column could never match")

	many := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		many = append(many, "event_type_number_"+strconv.Itoa(index)+"_padded_out_to_be_long")
	}
	_, err = normalizeEventTypes(many)
	require.Error(t, err, "a selection longer than its column must be refused, not truncated")
}
