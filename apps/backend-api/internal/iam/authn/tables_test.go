package authn

import (
	"reflect"
	"testing"

	"7link.com/platform/internal/iam/authn/query"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedDirectoryTables is the set the organization module publishes. authn
// cannot import organization -- organization imports authn, so the import is a
// cycle -- and a harness that runs authn alone has to publish what the owner
// would, the way the connectors do in a running deployment.
var publishedDirectoryTables = []string{
	"iam_groups",
	"iam_group_members",
	"iam_group_role_bindings",
	"iam_positions",
	"iam_position_members",
	"iam_position_role_bindings",
	"iam_departments",
	"iam_department_closure",
	"iam_member_departments",
	"iam_role_data_scopes",
	"iam_role_scope_departments",
	"iam_tenants",
}

// publishedForeignTables returns the sets the identity module publishes, in the
// shape core's connectors receive them.
func publishedForeignTables(prefix string) (gormx.TableSet, gormx.TableSet) {
	directory := make(map[string]gormx.TableName, len(publishedDirectoryTables))
	for _, logical := range publishedDirectoryTables {
		directory[logical] = gormx.NewTableName(prefix, logical)
	}
	return gormx.NewTableSet(directory), gormx.NewTableSet(map[string]gormx.TableName{
		"iam_principals": gormx.NewTableName(prefix, "iam_principals"),
	})
}

// boundMembershipChecker returns a tenant checker wired the way module wiring
// wires it. An unwired one reports ErrUnavailable instead of resolving a
// tenant from a table it cannot name, so a harness that expects an answer has
// to publish the names first.
func boundMembershipChecker(t *testing.T, runtime *appenv.Runtime) *core.MembershipChecker {
	t.Helper()
	checker := core.NewMembershipChecker(runtime)
	directory, principal := publishedForeignTables(runtime.TablePrefix())
	require.NoError(t, checker.BindForeignTables(directory, principal))
	return checker
}

// boundMemberTenantResolver returns the resolver module wiring hands to web
// sessions, bound to the same published names.
func boundMemberTenantResolver(t *testing.T, runtime *appenv.Runtime) core.MemberTenantResolver {
	t.Helper()
	return boundMembershipChecker(t, runtime).ResolveTenant
}

// boundAuthorizationEvaluator returns an evaluator wired the way module wiring
// wires it, for a harness that provides one to a module under test.
func boundAuthorizationEvaluator(t *testing.T, runtime *appenv.Runtime) core.AuthorizationEvaluator {
	t.Helper()
	authorizer := core.NewAuthorizer(runtime)
	directory, principal := publishedForeignTables(runtime.TablePrefix())
	require.NoError(t, authorizer.BindForeignTables(directory, principal))
	return authorizer
}

// TestTableSetPublishesPrefixedNames is the contract the modules that JOIN this
// module's tables depend on: every name this module binds is published, and
// every name it publishes carries the deployment prefix exactly once.
//
// A published name that lost its prefix would send a consumer to a table the
// deployment does not have, and the defect would only appear where a prefix is
// configured -- never in the tests that run without one.
func TestTableSetPublishesPrefixedNames(t *testing.T) {
	const prefix = "sl_"
	plain, prefixed := TableSet(""), TableSet(prefix)
	require.Positive(t, prefixed.Len(), "a module that owns tables publishes at least one")
	assert.Equal(t, plain.Len(), prefixed.Len(), "the prefix must not change what is published")

	for _, logical := range prefixed.Names() {
		name, ok := prefixed.Lookup(logical)
		require.True(t, ok, logical)
		assert.Equal(t, prefix+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)

		withoutPrefix, ok := plain.Lookup(logical)
		require.True(t, ok, logical)
		assert.Equal(t, logical, withoutPrefix.String(), logical)
	}
}

// TestCredentialTableNamesCoversEveryTableTheModuleOwns is the drift guard for the typed
// publication. The struct is written out by hand, so a table added to the module
// could be left out of it and no consumer would ever be able to ask for the
// name; comparing the field names against the query layer's own TableNames --
// which is generated from the models -- makes that a failing test rather than a
// silent gap.
func TestCredentialTableNamesCoversEveryTableTheModuleOwns(t *testing.T) {
	t.Parallel()

	published := reflect.TypeOf(CredentialTableNames{})
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
	bound := PublishedCredentialTableNames("sl_")
	boundValue := reflect.ValueOf(bound)
	ownedValue := reflect.ValueOf(query.BindNames("sl_"))
	for index := range published.NumField() {
		field := published.Field(index).Name
		assert.Equal(t,
			ownedValue.FieldByName(field).Interface(), boundValue.Field(index).Interface(), field)
	}
	assert.True(t, bound.Bound())
	assert.False(t, CredentialTableNames{}.Bound(), "the zero value must report that nothing arrived")
}
