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

// TestBindNamesCoversEveryBoundTable keeps Bind and BindNames from drifting
// apart: a table reachable through Bind but missing from BindNames would be
// invisible to every module that JOINs federation's tables.
func TestBindNamesCoversEveryBoundTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	published := reflect.ValueOf(BindNames("sl_"))
	require.Equal(t, 5, published.NumField())

	for _, table := range []string{
		Bind(db, "sl_").Providers.TableName(),
		Bind(db, "sl_").IdentityLinks.TableName(),
		Bind(db, "sl_").SAMLServiceProviders.TableName(),
		Bind(db, "sl_").SAMLIDPKeys.TableName(),
		Bind(db, "sl_").IdentityLinkTransactions.TableName(),
	} {
		found := false
		for index := range published.NumField() {
			if published.Field(index).Interface().(gormx.TableName).String() == table {
				found = true
				break
			}
		}
		assert.True(t, found, "%s is bound but not published", table)
	}
}
