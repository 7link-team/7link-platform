package core

import (
	"context"
	"reflect"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// publishedDirectoryTables is what the organization module publishes: every
// table a core authorization statement JOINs and core does not own. Core cannot
// import organization to ask it for the names -- core is activated before it --
// so the test fills in the struct core declared, the way the owner does.
func publishedDirectoryTables(prefix string) DirectoryTableNames {
	return DirectoryTableNames{
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
	}
}

// publishedPrincipalTable is what the identity module publishes.
func publishedPrincipalTable(prefix string) PrincipalTableNames {
	return PrincipalTableNames{Principals: gormx.NewTableName(prefix, "iam_principals")}
}

// joinedTestTables returns a holder wired the way a running deployment wires it.
func joinedTestTables(t *testing.T, prefix string) *joinedTables {
	t.Helper()
	tables := &joinedTables{}
	require.NoError(t, tables.bind(publishedDirectoryTables(prefix), publishedPrincipalTable(prefix)))
	return tables
}

// testAuthorizationTables returns the names a statement would read, for the
// helpers that take the assembled set rather than a holder.
func testAuthorizationTables(t *testing.T, prefix string) query.AuthorizationTables {
	t.Helper()
	names, err := joinedTestTables(t, prefix).authorizationTables(prefix)
	require.NoError(t, err)
	return names
}

// bindModuleTables pushes the published sets into a module's own holder, for a
// harness that runs core without the modules that own the tables it joins: the
// names are the ones organization and identity would publish.
func bindModuleTables(t *testing.T, module *Module, prefix string) {
	t.Helper()
	require.NoError(t, module.joined.bind(publishedDirectoryTables(prefix), publishedPrincipalTable(prefix)))
}

// boundGuard, boundChecker and boundAuthorizer wire a reader built outside the
// module the way the module's connectors wire it in a running deployment: an
// object a test builds itself starts unbound, and an unbound reader reports
// ErrUnavailable instead of joining a table it cannot name.
func boundGuard(t *testing.T, runtime *appenv.Runtime, prefix string) *tenantAdministratorGuard {
	t.Helper()
	guard := NewAdministratorGuard(runtime)
	guard.joined = joinedTestTables(t, prefix)
	return guard
}

func boundChecker(t *testing.T, runtime *appenv.Runtime, prefix string) *MembershipChecker {
	t.Helper()
	checker := NewMembershipChecker(runtime)
	checker.joined = joinedTestTables(t, prefix)
	return checker
}

func boundAuthorizer(t *testing.T, runtime *appenv.Runtime, prefix string) *Authorizer {
	t.Helper()
	authorizer := NewAuthorizer(runtime)
	authorizer.joined = joinedTestTables(t, prefix)
	return authorizer
}

// TestJoinedTablesBindBothOwners keeps the resolved names exactly the ones the
// owners published, prefix included. A name core invented from its own prefix
// would send an authorization statement to a table the deployment does not
// have, and the defect would appear only where a prefix is configured.
func TestJoinedTablesBindBothOwners(t *testing.T) {
	t.Parallel()

	tables := joinedTestTables(t, "sl_")

	assert.True(t, tables.bound())
	directory, err := tables.directoryNames()
	require.NoError(t, err)
	plain, err := joinedTestTables(t, "").directoryNames()
	require.NoError(t, err)

	// Every field the owner fills, not the two an assertion happens to name: the
	// bind copies them across one by one, so a field it drops would stay zero
	// and reach a statement as an empty table name.
	actual := map[string][2]string{
		"iam_groups":                 {plain.Groups.String(), directory.Groups.String()},
		"iam_group_members":          {plain.GroupMembers.String(), directory.GroupMembers.String()},
		"iam_group_role_bindings":    {plain.GroupRoleBindings.String(), directory.GroupRoleBindings.String()},
		"iam_positions":              {plain.Positions.String(), directory.Positions.String()},
		"iam_position_members":       {plain.PositionMembers.String(), directory.PositionMembers.String()},
		"iam_position_role_bindings": {plain.PositionRoleBindings.String(), directory.PositionRoleBindings.String()},
		"iam_departments":            {plain.Departments.String(), directory.Departments.String()},
		"iam_department_closure":     {plain.DepartmentClosure.String(), directory.DepartmentClosure.String()},
		"iam_member_departments":     {plain.MemberDepartments.String(), directory.MemberDepartments.String()},
		"iam_role_data_scopes":       {plain.RoleDataScopes.String(), directory.RoleDataScopes.String()},
		"iam_role_scope_departments": {plain.RoleScopeDepartments.String(), directory.RoleScopeDepartments.String()},
		"iam_tenants":                {plain.Tenants.String(), directory.Tenants.String()},
	}

	require.Len(t, actual, reflect.TypeOf(DirectoryTableNames{}).NumField())
	for logical, pair := range actual {
		assert.Equal(t, logical, pair[0], logical)
		assert.Equal(t, "sl_"+logical, pair[1], logical)
	}
	assert.Equal(t, "iam_groups", directory.Groups.Logical())
	assert.False(t, directory.Groups.IsZero())

	names, err := tables.authorizationTables("sl_")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_roles", names.Tables.Roles.String())
	assert.Equal(t, "sl_iam_member_departments", names.Directory.MemberDepartments.String())
	assert.Equal(t, "sl_iam_principals", names.Principal.String())
}

// TestJoinedTablesRejectAnOwnerThatDoesNotPublish proves a partial publication
// cannot pass as a complete one: a field the owner left empty is an error at
// bind time that names the field, and the holder stays unbound instead of
// keeping the half it did resolve.
//
// Every field is tried, not one. The binder lists them by hand, so a field
// added to the struct and forgotten there would otherwise reach a statement as
// an empty table name -- the defect the typed publication exists to stop.
func TestJoinedTablesRejectAnOwnerThatDoesNotPublish(t *testing.T) {
	t.Parallel()

	declared := reflect.TypeOf(DirectoryTableNames{})
	for index := range declared.NumField() {
		field := declared.Field(index).Name
		incomplete := publishedDirectoryTables("sl_")
		reflect.ValueOf(&incomplete).Elem().Field(index).SetZero()

		short := &joinedTables{}
		require.ErrorContains(t, short.bindDirectoryTables(incomplete), field)
		assert.False(t, short.bound(), field)
	}

	noPrincipal := &joinedTables{}
	err := noPrincipal.bindPrincipalTable(PrincipalTableNames{})
	require.ErrorContains(t, err, "Principals")
	assert.False(t, noPrincipal.bound())

	partial := &joinedTables{}
	err = partial.bind(publishedDirectoryTables("sl_"), PrincipalTableNames{})
	require.Error(t, err)
	assert.False(t, partial.bound(), "a half-bound holder must not report itself bound")
}

// TestJoinedTablesReportUnboundAsUnavailable is the behavioral guarantee the
// connectors exist for: a table-name set that never arrived is an explicit
// error, never an empty table name in a statement. Both accessors every reader
// goes through are checked, including on a nil holder.
func TestJoinedTablesReportUnboundAsUnavailable(t *testing.T) {
	t.Parallel()

	unbound := &joinedTables{}
	names, err := unbound.authorizationTables("sl_")
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Empty(t, names.Tables.Roles.String())
	directory, err := unbound.directoryNames()
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.True(t, directory.Groups.IsZero())

	var absent *joinedTables
	assert.False(t, absent.bound())
	_, err = absent.authorizationTables("sl_")
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = absent.directoryNames()
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, absent.bindDirectoryTables(publishedDirectoryTables("sl_")), ErrUnavailable)
	assert.ErrorIs(t, absent.bindPrincipalTable(publishedPrincipalTable("sl_")), ErrUnavailable)
}

// TestModuleReadersFailClosedUntilTheOwnersPublish is the guarantee the
// connectors exist for, proven on the module a deployment actually runs: with
// the owners absent from wiring, every reader that JOINs a table core does not
// own reports ErrUnavailable.
//
// ErrUnavailable specifically, not "some error": the harness database has the
// tables, so a statement built from an empty table name would come back as a
// SQL error instead, and the test would fail rather than pass for the wrong
// reason. No reader may reach the database before the names arrive.
func TestModuleReadersFailClosedUntilTheOwnersPublish(t *testing.T) {
	harness := newCoreServiceHarness(t)
	instance := NewModule(harness.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, harness.repository.ids))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, module.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: 11}))
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))

	ctx := t.Context()
	_, err := instance.members.ResolveTenant(ctx, 11, 0)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.members.ActiveDepartment(ctx, 1, 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.members.IsMemberActive(ctx, 1, 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.repository.GetMember(ctx, 1, 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, _, err = instance.repository.ListMembersPage(ctx, 1, MemberFilter{Limit: 10})
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.checker.Check(ctx, 1, "user_view", 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.administrators.Protect(ctx, harness.repository.writer(), 1)
	assert.ErrorIs(t, err, ErrUnavailable)

	// The write path rejects the same way, before it writes anything.
	_, err = instance.repository.PutMember(ctx, TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Status: MemberActive,
	})
	assert.ErrorIs(t, err, ErrUnavailable)
	var written int64
	require.NoError(t, harness.db.Table("sl_iam_tenant_members").Where("tenant_id = ?", 1).Count(&written).Error)
	assert.Zero(t, written, "a rejected write must not leave a row behind")

	// The owners publish, and the same readers serve the same calls.
	bindModuleTables(t, instance, "sl_")
	_, err = instance.members.ResolveTenant(ctx, 11, 0)
	require.NoError(t, err)
	_, err = instance.repository.GetMember(ctx, 1, 11)
	assert.ErrorIs(t, err, ErrMemberNotFound, "a bound reader reaches the table and reports its own answer")
	_, err = instance.repository.PutMember(ctx, TenantMember{
		TenantID: 1, PrincipalID: 11, DisplayName: "Member", Status: MemberActive,
	})
	require.NoError(t, err)
}

// TestJoinedTablesCarryTheOwnersNamesIntoEveryStatement proves the readers
// reach the bound names rather than re-deriving them: the repository and the
// evaluator hand out the same directory set the owner published.
func TestJoinedTablesCarryTheOwnersNamesIntoEveryStatement(t *testing.T) {
	t.Parallel()

	harness := newRepositoryHarness(t, "sl_")
	names, err := harness.repository.authorizationTables()
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_groups", names.Directory.Groups.String())
	directory, err := harness.repository.directoryTables()
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_member_departments", directory.MemberDepartments.String())

	authorizer := NewAuthorizer(harness.runtime)
	require.NoError(t, authorizer.BindForeignTables(
		publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_"),
	))
	evaluator, err := authorizer.authorizationTables()
	require.NoError(t, err)
	assert.Equal(t, names, evaluator)

	checker := NewMembershipChecker(harness.runtime)
	assert.ErrorContains(t, checker.BindForeignTables(
		DirectoryTableNames{Groups: gormx.NewTableName("sl_", "iam_groups")},
		publishedPrincipalTable("sl_"),
	), "GroupMembers")
	require.NoError(t, checker.BindForeignTables(
		publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_"),
	))
	membership, err := checker.authorizationTables()
	require.NoError(t, err)
	assert.Equal(t, names, membership)
}

// TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder covers the two
// guards every reader's bind carries, which a deployment reaches from opposite
// directions: a nil reader has nowhere to record the names and reports
// ErrUnavailable, and a reader built outside its constructor gets its holder
// created instead of a nil dereference on the first lookup.
func TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder(t *testing.T) {
	t.Parallel()

	directory := publishedDirectoryTables("sl_")
	principal := publishedPrincipalTable("sl_")

	var absentAuthorizer *Authorizer
	assert.ErrorIs(t, absentAuthorizer.BindForeignTables(directory, principal), ErrUnavailable)
	var absentChecker *MembershipChecker
	assert.ErrorIs(t, absentChecker.BindForeignTables(directory, principal), ErrUnavailable)

	authorizer := &Authorizer{}
	require.NoError(t, authorizer.BindForeignTables(directory, principal))
	evaluator, err := authorizer.authorizationTables()
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", evaluator.Directory.Tenants.String())

	checker := &MembershipChecker{}
	require.NoError(t, checker.BindForeignTables(directory, principal))
	assert.True(t, checker.joined.bound())
}
