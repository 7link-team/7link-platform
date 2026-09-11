package wuid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTableSetPublishesPrefixedNames is the contract the modules that JOIN this
// module's tables depend on: every name this module binds is published, and
// every name it publishes carries the deployment prefix exactly once.
//
// A published name that lost its prefix would send a consumer to a table the
// deployment does not have, and the defect would only appear where a prefix is
// configured -- never in the tests that run without one.
func TestTableSetPublishesPrefixedNames(t *testing.T) {
	const prefix = "sl_"
	plain, prefixed := TableSet(""), TableSet(prefix)
	require.Positive(t, prefixed.Len(), "a module that owns tables publishes at least one")
	assert.Equal(t, plain.Len(), prefixed.Len(), "the prefix must not change what is published")

	for _, logical := range prefixed.Names() {
		name, ok := prefixed.Lookup(logical)
		require.True(t, ok, logical)
		assert.Equal(t, prefix+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)

		withoutPrefix, ok := plain.Lookup(logical)
		require.True(t, ok, logical)
		assert.Equal(t, logical, withoutPrefix.String(), logical)
	}
}
