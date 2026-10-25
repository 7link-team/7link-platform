package model

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandDecisionPreservesSourceAndActorWithoutCredentials(t *testing.T) {
	require.Equal(t, "clientwork_command_decisions", (CommandDecision{}).TableName())
	typ := reflect.TypeFor[CommandDecision]()
	for _, name := range []string{"TenantID", "ID", "ProjectID", "TargetKind", "TargetID", "Action", "SourceID", "SourceVersion", "ActorID", "Reason", "CreatedAt"} {
		_, ok := typ.FieldByName(name)
		require.True(t, ok, name)
	}
	for _, name := range []string{"Cookie", "Proof", "ReauthProof"} {
		_, ok := typ.FieldByName(name)
		require.False(t, ok, name)
	}
}
