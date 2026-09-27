package authn

import (
	"encoding/json"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountViewIdentityCannotBeSuppliedByJSON(t *testing.T) {
	var claims Claims
	require.NoError(t, json.Unmarshal([]byte(`{"principal_id":"9","AccountView":{"ID":"50","PrincipalID":"7"},"account_view":{"ID":"50","PrincipalID":"7"}}`), &claims))
	assert.Nil(t, claims.AccountView)
	claims.AccountView = &AccountViewIdentity{ID: guid.ID(50), PrincipalID: guid.ID(7), ReadOnly: true}
	encoded, err := json.Marshal(claims)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "AccountView")
	assert.NotContains(t, string(encoded), "account_view")
}

func TestActorIdentityIsSeparateFromViewedPrincipal(t *testing.T) {
	assert.Zero(t, ActorIDFromContext(t.Context()))
	ctx := WithClaims(t.Context(), &Claims{PrincipalID: guid.ID(9)})
	assert.Equal(t, guid.ID(9), ActorIDFromContext(ctx))
	ctx = WithClaims(t.Context(), &Claims{
		PrincipalID: guid.ID(9),
		AccountView: &AccountViewIdentity{ID: guid.ID(50), PrincipalID: guid.ID(7), ReadOnly: true},
	})
	assert.Equal(t, guid.ID(7), ActorIDFromContext(ctx))
	assert.Equal(t, guid.ID(9), PrincipalIDFromContext(ctx))
}
