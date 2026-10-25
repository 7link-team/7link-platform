package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReviewTypeLogicalTableName(t *testing.T) {
	require.Equal(t, "delivery_review_types", (ReviewType{}).TableName())
}
