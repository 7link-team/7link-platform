package query

import (
	"reflect"
	"strings"
	"testing"

	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestBindNamesPrefixesEveryTable walks TableNames by reflection rather than
// naming its fields, so a table added to the struct but never wired into
// BindNames fails here. An unwired table keeps the zero TableName and resolves
// to the empty string, and a deployment that shares a database behind a prefix
// would then read whatever unprefixed table happens to carry that name.
func TestBindNamesPrefixesEveryTable(t *testing.T) {
	const prefix = "sl_"
	names := BindNames(prefix)
	value := reflect.ValueOf(names)

	require.NotZero(t, value.NumField())
	seen := make(map[string]string, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		name, ok := value.Field(index).Interface().(gormx.TableName)
		require.True(t, ok, field.Name)

		logical := name.Logical()
		assert.NotEmpty(t, logical, "%s was never wired into BindNames", field.Name)
		assert.Equal(t, prefix+logical, name.String(), field.Name)
		assert.True(t, strings.HasPrefix(name.String(), prefix), field.Name)

		// Two fields resolving to the same table would make one of them read
		// the other's rows.
		if previous, duplicate := seen[logical]; duplicate {
			t.Errorf("%s and %s both map to %s", previous, field.Name, logical)
		}
		seen[logical] = field.Name
	}
}

func TestBindNamesWithoutAPrefixKeepsTheLogicalName(t *testing.T) {
	names := BindNames("")
	value := reflect.ValueOf(names)
	for index := 0; index < value.NumField(); index++ {
		name := value.Field(index).Interface().(gormx.TableName)
		// An unprefixed deployment must address the plain table, not an empty
		// or doubled name.
		assert.Equal(t, name.Logical(), name.String(), value.Type().Field(index).Name)
	}
}

// TestBindAppliesThePrefixToEveryQuery checks the other half of the contract:
// BindNames computing the right names is useless if Bind forgets to hand one
// of them to its query. A query left on the generated default would address the
// unprefixed table while every other query in the same request used the prefix.
func TestBindAppliesThePrefixToEveryQuery(t *testing.T) {
	const prefix = "sl_"
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	bound := Bind(db, prefix)
	names := BindNames(prefix)
	expected := make(map[string]bool, reflect.ValueOf(names).NumField())
	for index := 0; index < reflect.ValueOf(names).NumField(); index++ {
		expected[reflect.ValueOf(names).Field(index).Interface().(gormx.TableName).String()] = false
	}

	value := reflect.ValueOf(bound).Elem()
	tables := 0
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		method := field.Addr().MethodByName("TableName")
		if !method.IsValid() {
			continue
		}
		table, ok := method.Call(nil)[0].Interface().(string)
		require.True(t, ok)
		tables++
		assert.True(t, strings.HasPrefix(table, prefix),
			"%s addresses %q without the prefix", value.Type().Field(index).Name, table)
		if _, declared := expected[table]; declared {
			expected[table] = true
		}
	}
	require.NotZero(t, tables, "no query exposed a table name")

	for table, bound := range expected {
		assert.True(t, bound, "%s is declared in BindNames but no query was bound to it", table)
	}
}
