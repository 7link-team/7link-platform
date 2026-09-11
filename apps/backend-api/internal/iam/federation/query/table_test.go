package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryFederationTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"providers":     {plain.Providers.TableName(), prefixed.Providers.TableName()},
		"links":         {plain.IdentityLinks.TableName(), prefixed.IdentityLinks.TableName()},
		"saml-sps":      {plain.SAMLServiceProviders.TableName(), prefixed.SAMLServiceProviders.TableName()},
		"saml-idp-keys": {plain.SAMLIDPKeys.TableName(), prefixed.SAMLIDPKeys.TableName()},
		"transactions":  {plain.IdentityLinkTransactions.TableName(), prefixed.IdentityLinkTransactions.TableName()},
	}
	require.Len(t, actual, 5)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}

// TestBindNamesCoversEveryBoundTable keeps Bind and the published names from
// drifting apart: a table reachable through Bind but missing from BindNames
// would be invisible to every module that JOINs federation's tables.
func TestBindNamesCoversEveryBoundTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	names := BindNames("sl_")
	published := names.Map()
	require.Len(t, published, 5)

	for logical, name := range published {
		assert.Equal(t, "sl_"+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)
	}

	bound := []string{
		Bind(db, "sl_").Providers.TableName(),
		Bind(db, "sl_").IdentityLinks.TableName(),
		Bind(db, "sl_").SAMLServiceProviders.TableName(),
		Bind(db, "sl_").SAMLIDPKeys.TableName(),
		Bind(db, "sl_").IdentityLinkTransactions.TableName(),
	}
	for _, table := range bound {
		found := false
		for _, name := range published {
			if name.String() == table {
				found = true
				break
			}
		}
		assert.True(t, found, "%s is bound but not published", table)
	}
}
