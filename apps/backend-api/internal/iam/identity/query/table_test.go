package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryIdentityTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"principals":                  {plain.Principals.TableName(), prefixed.Principals.TableName()},
		"principal_lifecycles":        {plain.PrincipalLifecycles.TableName(), prefixed.PrincipalLifecycles.TableName()},
		"service_accounts":            {plain.ServiceAccounts.TableName(), prefixed.ServiceAccounts.TableName()},
		"service_account_credentials": {plain.ServiceAccountCredentials.TableName(), prefixed.ServiceAccountCredentials.TableName()},
	}
	require.Len(t, actual, 4)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
