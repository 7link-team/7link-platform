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

// TestBindAppliesThePrefix covers the binding path itself: a table name that
// reached the generated query object without the prefix would read the wrong
// table in a prefixed deployment and the same one in every test that runs
// without a prefix.
func TestBindAppliesThePrefix(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	assert.Equal(t, "sl_"+plain.TableName(), prefixed.TableName())
}
