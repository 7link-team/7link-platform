package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryGovernanceTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"requests": {plain.AccessRequests.TableName(), prefixed.AccessRequests.TableName()},
		"steps":    {plain.ApprovalSteps.TableName(), prefixed.ApprovalSteps.TableName()},
		"reviews":  {plain.AccessReviews.TableName(), prefixed.AccessReviews.TableName()},
		"items":    {plain.AccessReviewItems.TableName(), prefixed.AccessReviewItems.TableName()},
	}
	require.Len(t, actual, 4)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
