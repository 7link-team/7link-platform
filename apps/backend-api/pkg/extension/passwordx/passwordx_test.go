package passwordx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
	require.NoError(t, err)
	assert.NotContains(t, hash, "correct horse")
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"))
	ok, err := Verify(hash, "correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = Verify(hash, "wrong")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerifyAcceptsSourceGoldenHash(t *testing.T) {
	const golden = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$gy5SuVm5Z7Vw7keB9se9p87QGcomaseB/S2U1OhTsM0"
	ok, err := Verify(golden, "correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = Verify(golden, "wrong")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRejectsInvalidInput(t *testing.T) {
	_, err := Hash("")
	assert.Error(t, err)
	_, err = Verify("bad", "password")
	assert.ErrorIs(t, err, ErrInvalidHash)
}

func TestVerifyRejectsMalformedArgon2Parameters(t *testing.T) {
	validSalt := "MTIzNDU2Nzg5MGFiY2RlZg"
	validHash := "MTIzNDU2Nzg5MGFiY2RlZjEyMzQ1Njc4OTBhYmNkZWY"
	for _, encoded := range []string{
		"$argon2id$v=19$broken$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=abc,t=2,p=1$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=19456,t=2,x=1$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=1,t=2,p=1$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=1048576,t=100,p=32$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=19456,t=3,p=1$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=19456,t=2,p=2$" + validSalt + "$" + validHash,
		"$argon2id$v=19$m=19456,t=2,p=1$" + validSalt + "YQ$" + validHash,
		"$argon2id$v=19$m=19456,t=2,p=1$" + validSalt + "$" + validHash + "YQ",
		"$argon2id$v=19$m=19456,t=2,p=1$invalid$" + validHash,
		"$argon2id$v=19$m=19456,t=2,p=1$MTIz$" + validHash,
		"$argon2id$v=19$m=19456,t=2,p=1$" + validSalt + "$invalid",
		"$argon2id$v=19$m=19456,t=2,p=1$" + validSalt + "$MTIz",
	} {
		_, err := Verify(encoded, "password")
		assert.ErrorIs(t, err, ErrInvalidHash, encoded)
	}
}
