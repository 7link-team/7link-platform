package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryProvisioningTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"directories":      {plain.Directories.TableName(), prefixed.Directories.TableName()},
		"credentials":      {plain.Credentials.TableName(), prefixed.Credentials.TableName()},
		"resources":        {plain.Resources.TableName(), prefixed.Resources.TableName()},
		"targets":          {plain.Targets.TableName(), prefixed.Targets.TableName()},
		"target resources": {plain.TargetResources.TableName(), prefixed.TargetResources.TableName()},
	}
	require.Len(t, actual, 5)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
