package guid

import (
	"testing"

	platformguid "7link.com/platform/pkg/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The alias must stay the same type as pkg/guid.ID: a distinct type would
// split every value crossing between internal/ and the extension packages.
func TestIDIsThePlatformIdentifier(t *testing.T) {
	var id ID = platformguid.ID(42)
	assert.Equal(t, platformguid.ID(42), id)

	parsed, err := Parse("123")
	require.NoError(t, err)
	assert.Equal(t, ID(123), parsed)

	_, err = Parse("0")
	assert.Error(t, err)
}
