package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHandoverArtifactSystemProducerRemainsNullable(t *testing.T) {
	r := HandoverArtifact{Source: "SYSTEM"}
	require.Nil(t, r.ExecutionID)
	b, err := json.Marshal(r)
	require.NoError(t, err)
	require.Contains(t, string(b), `"ExecutionID":null`)
	require.Equal(t, "execution_handover_artifacts", r.TableName())
}
