package gormx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableNameAppliesThePrefixOnce(t *testing.T) {
	plain := NewTableName("", "iam_roles")
	assert.Equal(t, "iam_roles", plain.String())
	assert.Equal(t, "iam_roles", plain.Logical())
	assert.False(t, plain.IsZero())

	prefixed := NewTableName("sl_", "iam_roles")
	assert.Equal(t, "sl_iam_roles", prefixed.String())
	assert.Equal(t, "iam_roles", prefixed.Logical(), "the logical name is what error messages should say")
}

// TestTableNameZeroIsObservable pins the zero value: a name that was never
// constructed must be detectable, because the alternative is an empty string
// traveling into a statement and reading whatever the dialect makes of it.
func TestTableNameZeroIsObservable(t *testing.T) {
	var zero TableName
	assert.True(t, zero.IsZero())
	assert.Empty(t, zero.String())
	assert.Empty(t, zero.Logical())
}

func TestTableSetLookupReportsNamesItDoesNotHold(t *testing.T) {
	set := NewTableSet(map[string]TableName{
		"iam_roles": NewTableName("sl_", "iam_roles"),
	})
	require.Equal(t, 1, set.Len())

	name, ok := set.Lookup("iam_roles")
	require.True(t, ok)
	assert.Equal(t, "sl_iam_roles", name.String())

	// The consumer asked for a table the publisher does not own. Returning a
	// zero name here would be a query against nothing; ok is what lets the
	// caller fail instead.
	missing, ok := set.Lookup("iam_principals")
	assert.False(t, ok)
	assert.True(t, missing.IsZero())
}

func TestTableSetNamesReportsWhatWasPublished(t *testing.T) {
	set := NewTableSet(map[string]TableName{
		"iam_roles":          NewTableName("sl_", "iam_roles"),
		"iam_tenant_members": NewTableName("sl_", "iam_tenant_members"),
	})
	// Sorted, so a caller can compare or log a set without ordering noise.
	assert.Equal(t, []string{"iam_roles", "iam_tenant_members"}, set.Names())
	assert.Empty(t, NewTableSet(nil).Names())
}

// TestTableSetCopyIsIndependent keeps a caller from editing the set it was
// handed, which would let a consumer rename a table its owner published.
func TestTableSetCopyIsIndependent(t *testing.T) {
	names := map[string]TableName{"iam_roles": NewTableName("", "iam_roles")}
	set := NewTableSet(names)
	names["iam_roles"] = NewTableName("evil_", "iam_roles")

	name, ok := set.Lookup("iam_roles")
	require.True(t, ok)
	assert.Equal(t, "iam_roles", name.String())
}
