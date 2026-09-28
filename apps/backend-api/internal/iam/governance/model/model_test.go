package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface{ TableName() string }

func TestGovernanceModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{AccessRequest{}, ApprovalStep{}, AccessReview{}, AccessReviewItem{}}
	expected := []string{"iam_access_requests", "iam_approval_steps", "iam_access_reviews", "iam_access_review_items"}
	require.Len(t, models, 4)
	for index, item := range models {
		assert.Equal(t, expected[index], item.TableName())
	}
}
