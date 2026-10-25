package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReviewPlanDoesNotExposeRawContentStorage(t *testing.T) {
	b, e := json.Marshal(ReviewPlan{Content: "raw-storage", SourceKind: "HUMAN"})
	require.NoError(t, e)
	require.NotContains(t, string(b), "raw-storage")
	require.Contains(t, string(b), "HUMAN")
	require.Equal(t, "delivery_plans", (ReviewPlan{}).TableName())
}
