package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQualityEscapeDoesNotExposeRawJSONStorage(t *testing.T) {
	b, e := json.Marshal(QualityEscape{PassedBy: "sensitive-json", MemoryIDs: "draft-json"})
	require.NoError(t, e)
	require.NotContains(t, string(b), "sensitive-json")
	require.NotContains(t, string(b), "draft-json")
	require.Equal(t, "governance_quality_escapes", (QualityEscape{}).TableName())
}
