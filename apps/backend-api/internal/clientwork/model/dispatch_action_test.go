package model

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatchActionStoresProvenanceWithoutCredentialMaterial(t *testing.T) {
	require.Equal(t, "clientwork_dispatch_actions", (DispatchAction{}).TableName())
	typ := reflect.TypeFor[DispatchAction]()
	for _, name := range []string{"Cookie", "Token", "Proof", "Credential", "SessionID"} {
		_, exists := typ.FieldByName(name)
		require.False(t, exists, "immutable decisions must not store %s", name)
	}
}
