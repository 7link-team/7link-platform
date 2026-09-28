package authz

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profileTestChecker struct {
	constraint FieldConstraint
	err        error
}

func (c profileTestChecker) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (FieldConstraint, error) {
	return c.constraint, c.err
}

func TestProfileCatalogProjectionAndWriteBoundaries(t *testing.T) {
	registry := MustRegistry()
	constraint := FieldConstraint{Fields: map[string]FieldDecision{}}
	for _, field := range ProfileFields() {
		require.NoError(t, registry.RegisterField(field))
		constraint.Fields[field.Name] = DecideField(field, nil, nil)
	}
	assert.Len(t, registry.Fields(ProfileResource), 4)
	require.NoError(t, constraint.RequireRead("id", "login_name", "display_name", "email"))
	require.NoError(t, constraint.RequireWrite(true, "login_name", "display_name", "email"))
	require.NoError(t, constraint.RequireWrite(false, "login_name", "display_name", "email"))
	assert.ErrorIs(t, constraint.RequireWrite(true, "id"), ErrFieldDenied)
	assert.ErrorIs(t, constraint.RequireWrite(false, "id"), ErrFieldDenied)
	assert.ErrorIs(t, constraint.RequireRead("unknown"), ErrFieldDenied)
	assert.ErrorIs(t, constraint.RequireWrite(false, "unknown"), ErrFieldDenied)
	assert.Empty(t, *constraint.ReadString("email", ""))
	assert.False(t, *constraint.EmailVerified(false))
	for _, read := range []string{"omit", "redact", "unknown"} {
		constraint.Fields["email"] = FieldDecision{Read: read}
		assert.ErrorIs(t, constraint.RequireRead("email"), ErrFieldDenied)
		assert.Nil(t, constraint.EmailVerified(true))
		if read == "redact" {
			assert.Equal(t, "[REDACTED]", *constraint.ReadString("email", "sensitive@example.test"))
		} else {
			assert.Nil(t, constraint.ReadString("email", "sensitive@example.test"))
		}
	}
	_, err := ProfileConstraint(t.Context(), profileTestChecker{constraint: constraint}, 1, 11)
	require.NoError(t, err)
	for _, ids := range [][2]guid.ID{{0, 11}, {1, 0}, {-1, 11}, {1, -1}} {
		_, err := ProfileConstraint(t.Context(), profileTestChecker{}, ids[0], ids[1])
		assert.ErrorIs(t, err, ErrFieldDenied)
	}
	_, err = ProfileConstraint(t.Context(), nil, 1, 11)
	assert.ErrorIs(t, err, ErrFieldsUnavailable)
	_, err = ProfileConstraint(t.Context(), profileTestChecker{err: assert.AnError}, 1, 11)
	assert.ErrorIs(t, err, ErrFieldsUnavailable)
	assert.ErrorIs(t, err, assert.AnError)
	_, err = ProfileConstraint(t.Context(), profileTestChecker{}, 1, 11)
	assert.ErrorIs(t, err, ErrFieldDenied)
}
