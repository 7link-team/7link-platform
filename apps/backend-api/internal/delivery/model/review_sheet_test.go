package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReviewSheetLogicalTableName(t *testing.T) {
	require.Equal(t, "delivery_review_sheets", (ReviewSheet{}).TableName())
}
