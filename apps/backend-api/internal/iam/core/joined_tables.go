package core

import (
	"fmt"

	"7link.com/platform/internal/iam/core/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// joinedTables holds the physical names of the tables core JOINs but does not
// own, exactly as the modules that own them publish them.
//
// Core cannot resolve these names from its own deployment prefix the way it
// resolves its own tables. Importing the owning modules is not an option:
// pkg/module's Activate resolves a total order over module dependencies, core
// is activated before identity and organization, and the reverse import would
// make that order unsatisfiable -- a failure at startup rather than at review.
//
// So the owners push their sets in through the connectors core publishes, and
// core keeps asking for the names it knows by their logical name. The zero
// value is unbound, and every reader reports that as ErrUnavailable: a name
// that never arrived is an error, never an empty table name in a statement.
type joinedTables struct {
	directory query.DirectoryTables
	principal gormx.TableName

	directoryBound bool
	principalBound bool
}

// bindDirectoryTables records the names the organization module filled in.
//
// A field core declared and the owner left empty is rejected here rather than
// carried into a statement: the JOIN it would build reads a table the
// deployment does not have.
func (t *joinedTables) bindDirectoryTables(published DirectoryTableNames) error {
	if t == nil {
		return ErrUnavailable
	}
	for _, field := range []struct {
		name  gormx.TableName
		field string
	}{
		{published.Groups, "Groups"},
		{published.GroupMembers, "GroupMembers"},
		{published.GroupRoleBindings, "GroupRoleBindings"},
		{published.Positions, "Positions"},
		{published.PositionMembers, "PositionMembers"},
		{published.PositionRoleBindings, "PositionRoleBindings"},
		{published.Departments, "Departments"},
		{published.DepartmentClosure, "DepartmentClosure"},
		{published.MemberDepartments, "MemberDepartments"},
		{published.RoleDataScopes, "RoleDataScopes"},
		{published.RoleScopeDepartments, "RoleScopeDepartments"},
		{published.Tenants, "Tenants"},
	} {
		if field.name.IsZero() {
			return fmt.Errorf("bind the directory table names: organization left %s empty", field.field)
		}
	}
	t.directory = query.DirectoryTables{
		Groups:               published.Groups,
		GroupMembers:         published.GroupMembers,
		GroupRoleBindings:    published.GroupRoleBindings,
		Positions:            published.Positions,
		PositionMembers:      published.PositionMembers,
		PositionRoleBindings: published.PositionRoleBindings,
		Departments:          published.Departments,
		DepartmentClosure:    published.DepartmentClosure,
		MemberDepartments:    published.MemberDepartments,
		RoleDataScopes:       published.RoleDataScopes,
		RoleScopeDepartments: published.RoleScopeDepartments,
		Tenants:              published.Tenants,
	}
	t.directoryBound = true
	return nil
}

// bindPrincipalTable records the name the identity module filled in.
func (t *joinedTables) bindPrincipalTable(published PrincipalTableNames) error {
	if t == nil {
		return ErrUnavailable
	}
	if published.Principals.IsZero() {
		return fmt.Errorf("bind the principal table name: identity left Principals empty")
	}
	t.principal, t.principalBound = published.Principals, true
	return nil
}

// bind records both in one step, for a caller that builds an evaluator outside
// the module and has to supply them itself.
func (t *joinedTables) bind(directory DirectoryTableNames, principal PrincipalTableNames) error {
	if err := t.bindDirectoryTables(directory); err != nil {
		return err
	}
	return t.bindPrincipalTable(principal)
}

// bound reports whether both owners have published. Until they have, no
// statement that reads a table core does not own may run.
func (t *joinedTables) bound() bool {
	return t != nil && t.directoryBound && t.principalBound
}

// authorizationTables assembles one statement's names from core's deployment
// prefix and from the two published sets.
func (t *joinedTables) authorizationTables(prefix string) (query.AuthorizationTables, error) {
	if !t.bound() {
		return query.AuthorizationTables{}, ErrUnavailable
	}
	return query.BindAuthorizationTables(prefix, t.directory, t.principal), nil
}

// directoryNames returns the published directory names, or ErrUnavailable when
// the organization module never published them.
func (t *joinedTables) directoryNames() (query.DirectoryTables, error) {
	if t == nil || !t.directoryBound {
		return query.DirectoryTables{}, ErrUnavailable
	}
	return t.directory, nil
}
