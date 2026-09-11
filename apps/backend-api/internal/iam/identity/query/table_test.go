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
		"closure_requests":            {plain.ClosureRequests.TableName(), prefixed.ClosureRequests.TableName()},
		"principals":                  {plain.Principals.TableName(), prefixed.Principals.TableName()},
		"principal_lifecycles":        {plain.PrincipalLifecycles.TableName(), prefixed.PrincipalLifecycles.TableName()},
		"service_accounts":            {plain.ServiceAccounts.TableName(), prefixed.ServiceAccounts.TableName()},
		"service_account_credentials": {plain.ServiceAccountCredentials.TableName(), prefixed.ServiceAccountCredentials.TableName()},
	}
	require.Len(t, actual, 5)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}

// TestBindNamesMapPublishesPrefixedNames keeps the published set and the bound
// queries from drifting apart: a table reachable through Bind but missing from
// the map would be invisible to every consumer, and a name in the map that
// Bind does not bind would point at a table this module does not own.
func TestBindNamesMapPublishesPrefixedNames(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	published := BindNames(prefix).Map()
	require.NotEmpty(t, published, "a module that binds tables publishes them")
	for logical, name := range published {
		assert.Equal(t, prefix+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)
	}
}
