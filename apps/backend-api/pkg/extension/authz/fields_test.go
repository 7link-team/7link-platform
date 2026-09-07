package authz

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldPolicyCanonicalAndDenyPrecedence(t *testing.T) {
	f, ok := DefaultRegistry().FindField("todo", "description")
	require.True(t, ok)
	p := FieldPolicy{Read: FieldAccess{Default: "deny", AllowRoles: []guid.ID{2, 1, 1}, DenyRoles: []guid.ID{2}}, Write: FieldAccess{Default: "allow", DenyRoles: []guid.ID{1}}, Mask: "redact"}
	canonical, err := p.Canonical(f)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{1, 2}, canonical.Read.AllowRoles)
	assert.Equal(t, []guid.ID{2, 1, 1}, p.Read.AllowRoles, "canonicalization must not mutate the caller")
	assert.Equal(t, FieldDecision{Read: "allow", Create: false, Update: false}, DecideField(f, &canonical, []guid.ID{1}))
	assert.Equal(t, FieldDecision{Read: "redact", Create: false, Update: false}, DecideField(f, &canonical, []guid.ID{1, 2}))
	assert.Equal(t, FieldDecision{Read: "redact", Create: true, Update: true}, DecideField(f, &canonical, nil))
	for _, role := range []guid.ID{0, -1} {
		p.Read.AllowRoles = []guid.ID{role}
		_, err := p.Canonical(f)
		assert.Error(t, err)
	}
}

func TestFieldCapabilitiesCannotExposeSecretsOrMutateIdentifiers(t *testing.T) {
	for _, f := range DefaultFields() {
		if f.Name == "id" || f.Name == "version" {
			_, err := (FieldPolicy{}).Canonical(f)
			assert.Error(t, err)
			assert.Equal(t, FieldDecision{Read: "allow"}, DecideField(f, nil, nil))
		}
	}
	secret := Field{Resource: "test", Name: "password", Mutability: "writeOnly", Returned: "never", Configurable: true}
	assert.Equal(t, FieldDecision{Read: "omit", Create: true, Update: true}, DecideField(secret, nil, nil))
	p := FieldPolicy{Read: FieldAccess{Default: "allow"}, Write: FieldAccess{Default: "allow"}, Mask: "omit"}
	_, err := p.Canonical(secret)
	assert.Error(t, err)
	readonly := Field{Resource: "test", Name: "created_at", Mutability: "readOnly", Returned: "default", Configurable: true}
	_, err = p.Canonical(readonly)
	assert.Error(t, err)
	immutable := Field{Resource: "test", Name: "login", Mutability: "immutable", Returned: "default", Configurable: true}
	assert.Equal(t, FieldDecision{Read: "allow", Create: true}, DecideField(immutable, nil, nil))
	registry, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterField(secret))
	assert.Error(t, registry.RegisterField(secret))
	assert.Error(t, registry.RegisterField(Field{Resource: "sql;", Name: "name"}))
	assert.Empty(t, registry.Fields("unknown"))
}
