package authn

import (
	"reflect"
	"testing"

	"7link.com/platform/internal/iam/authn/query"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedForeignTables returns the names the organization and identity
// modules fill into the shapes core declared for the tables its authorization
// statements JOIN. authn cannot import organization -- organization imports
// authn, so the import is a cycle -- so a harness that wires one of core's
// readers here has to fill the shape the owner would. identity is importable,
// so its name comes from the owner's own constructor.
func publishedForeignTables(prefix string) (core.DirectoryTableNames, core.PrincipalTableNames) {
	return core.DirectoryTableNames{
			Groups:               gormx.NewTableName(prefix, "iam_groups"),
			GroupMembers:         gormx.NewTableName(prefix, "iam_group_members"),
			GroupRoleBindings:    gormx.NewTableName(prefix, "iam_group_role_bindings"),
			Positions:            gormx.NewTableName(prefix, "iam_positions"),
			PositionMembers:      gormx.NewTableName(prefix, "iam_position_members"),
			PositionRoleBindings: gormx.NewTableName(prefix, "iam_position_role_bindings"),
			Departments:          gormx.NewTableName(prefix, "iam_departments"),
			DepartmentClosure:    gormx.NewTableName(prefix, "iam_department_closure"),
			MemberDepartments:    gormx.NewTableName(prefix, "iam_member_departments"),
			RoleDataScopes:       gormx.NewTableName(prefix, "iam_role_data_scopes"),
			RoleScopeDepartments: gormx.NewTableName(prefix, "iam_role_scope_departments"),
			Tenants:              gormx.NewTableName(prefix, "iam_tenants"),
		}, core.PrincipalTableNames{
			Principals: identity.PublishedPrincipalTableNames(prefix).Principals,
		}
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

// TestCredentialTableNamesCarryThePrefixExactlyOnce is the contract the
// modules that JOIN this module's tables depend on: every name this module
// publishes carries the deployment prefix exactly once.
//
// A published name that lost its prefix would send a consumer to a table the
// deployment does not have, and the defect would only appear where a prefix is
// configured -- never in the tests that run without one.
func TestCredentialTableNamesCarryThePrefixExactlyOnce(t *testing.T) {
	t.Parallel()

	const prefix = "sl_"
	plain := reflect.ValueOf(PublishedCredentialTableNames(""))
	prefixed := reflect.ValueOf(PublishedCredentialTableNames(prefix))
	require.Positive(t, prefixed.NumField(), "a module that owns tables publishes at least one")

	for index := range prefixed.NumField() {
		field := prefixed.Type().Field(index).Name
		name, ok := prefixed.Field(index).Interface().(gormx.TableName)
		require.True(t, ok, field)
		assert.False(t, name.IsZero(), field)
		assert.Equal(t, prefix+name.Logical(), name.String(), field)

		withoutPrefix, ok := plain.Field(index).Interface().(gormx.TableName)
		require.True(t, ok, field)
		assert.Equal(t, name.Logical(), withoutPrefix.String(), field)
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
