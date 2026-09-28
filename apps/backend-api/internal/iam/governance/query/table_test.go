package query

import (
	"reflect"
	"testing"

	gormx "7link.com/platform/pkg/extension/gorm"
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

// TestBindNamesCarryThePrefixExactlyOnce reads every name BindNames produces,
// field by field, at a prefix and without one. A name that lost the prefix
// would read the wrong table in a prefixed deployment and the right one in
// every test that runs without a prefix -- so the two have to be compared.
func TestBindNamesCarryThePrefixExactlyOnce(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	plain, prefixed := reflect.ValueOf(BindNames("")), reflect.ValueOf(BindNames(prefix))
	names := prefixed.Type()
	require.Positive(t, names.NumField(), "a module that binds tables names at least one")

	for index := range names.NumField() {
		field := names.Field(index).Name
		name := prefixed.Field(index).Interface().(gormx.TableName)
		logical := plain.Field(index).Interface().(gormx.TableName).String()
		assert.Equal(t, prefix+logical, name.String(), field)
		assert.Equal(t, logical, name.Logical(), field)
		assert.False(t, name.IsZero(), field)
	}
}
