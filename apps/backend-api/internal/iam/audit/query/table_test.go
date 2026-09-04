package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryAuditTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"events":             {plain.Events.TableName(), prefixed.Events.TableName()},
		"heads":              {plain.Heads.TableName(), prefixed.Heads.TableName()},
		"retention_policies": {plain.RetentionPolicies.TableName(), prefixed.RetentionPolicies.TableName()},
	}
	require.Len(t, actual, 3)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
