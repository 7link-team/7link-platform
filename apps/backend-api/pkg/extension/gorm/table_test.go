package gormx

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
