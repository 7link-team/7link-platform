package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReviewOpinionLogicalTableName(t *testing.T) {
	require.Equal(t, "delivery_review_opinions", (ReviewOpinion{}).TableName())
}
