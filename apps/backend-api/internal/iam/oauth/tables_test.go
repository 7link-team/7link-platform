package oauth

import (
	"reflect"
	"testing"

	"7link.com/platform/internal/iam/oauth/query"
	gormx "7link.com/platform/pkg/extension/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGrantTableNamesCarryThePrefixExactlyOnce is the contract the modules that
// JOIN this module's tables depend on: every name this module binds is
// published, and every name it publishes carries the deployment prefix exactly
// once.
//
// A published name that lost its prefix would send a consumer to a table the
// deployment does not have, and the defect would only appear where a prefix is
// configured -- never in the tests that run without one.
func TestGrantTableNamesCarryThePrefixExactlyOnce(t *testing.T) {
	const prefix = "sl_"
	plain := reflect.ValueOf(PublishedGrantTableNames(""))
	prefixed := reflect.ValueOf(PublishedGrantTableNames(prefix))
	published := prefixed.Type()
	require.Positive(t, published.NumField(), "a module that owns tables publishes at least one")

	for index := range published.NumField() {
		field := published.Field(index).Name
		name := prefixed.Field(index).Interface().(gormx.TableName)
		withoutPrefix := plain.Field(index).Interface().(gormx.TableName)
		assert.Equal(t, prefix+withoutPrefix.String(), name.String(), field)
		assert.Equal(t, withoutPrefix.String(), name.Logical(), field)
		assert.False(t, name.IsZero(), field)
	}
}

// TestGrantTableNamesCoversEveryTableTheModuleOwns is the drift guard for the typed
// publication. The struct is written out by hand, so a table added to the module
// could be left out of it and no consumer would ever be able to ask for the
// name; comparing the field names against the query layer's own TableNames --
// which is generated from the models -- makes that a failing test rather than a
// silent gap.
func TestGrantTableNamesCoversEveryTableTheModuleOwns(t *testing.T) {
	t.Parallel()

	published := reflect.TypeOf(GrantTableNames{})
	owned := reflect.TypeOf(query.BindNames("sl_"))
	require.Equal(t, owned.NumField(), published.NumField(),
		"the published names must cover every table the module owns")

	names := make(map[string]bool, owned.NumField())
	for index := range owned.NumField() {
		names[owned.Field(index).Name] = true
	}
	for index := range published.NumField() {
		field := published.Field(index).Name
		assert.True(t, names[field], "%s is published but the module does not own it", field)
	}

	// And the values are the owner's own, under the owner's own prefix.
	bound := PublishedGrantTableNames("sl_")
	boundValue := reflect.ValueOf(bound)
	ownedValue := reflect.ValueOf(query.BindNames("sl_"))
	for index := range published.NumField() {
		field := published.Field(index).Name
		assert.Equal(t,
			ownedValue.FieldByName(field).Interface(), boundValue.Field(index).Interface(), field)
	}
	assert.True(t, bound.Bound())
	assert.False(t, GrantTableNames{}.Bound(), "the zero value must report that nothing arrived")
}
