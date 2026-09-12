package core

import (
	"fmt"

	"7link.com/platform/internal/iam/core/query"
)

// foreignTables holds the physical names of the tables core JOINs but does not
// own, exactly as the modules that own them fill them in.
//
// Core cannot resolve these names from its own deployment prefix the way it
// resolves its own tables. Importing the owning modules is not an option:
// pkg/module's Activate resolves a total order over module dependencies, core
// is activated before identity and organization, and the reverse import would
// make that order unsatisfiable -- a failure at startup rather than at review.
//
// So core declares the shape it needs and the owners fill it through the
// connectors core publishes. The zero value is unbound, and every reader
// reports that as ErrUnavailable: a name that never arrived is an error, never
// an empty table name in a statement.
//
// Boundness is read off the names themselves rather than tracked in a flag
// beside them, because a flag and the names it describes can disagree.
type foreignTables struct {
	directory DirectoryTableNames
	principal PrincipalTableNames
}

// bindDirectory records the names the organization module filled in.
//
// A field core declared and the owner left empty is rejected here rather than
// carried into a statement: the JOIN it would build reads a table the
// deployment does not have.
func (t *foreignTables) bindDirectory(published DirectoryTableNames) error {
	if t == nil {
		return ErrUnavailable
	}
	for _, entry := range published.each() {
		if entry.name.IsZero() {
			return fmt.Errorf("bind the directory table names: organization left %s empty", entry.field)
		}
	}
	t.directory = published
	return nil
}

// bindPrincipal records the name the identity module filled in.
func (t *foreignTables) bindPrincipal(published PrincipalTableNames) error {
	if t == nil {
		return ErrUnavailable
	}
	if published.Principals.IsZero() {
		return fmt.Errorf("bind the principal table name: identity left Principals empty")
	}
	t.principal = published
	return nil
}

// bind records both in one step, for a caller that builds an evaluator outside
// the module and has to supply them itself.
func (t *foreignTables) bind(directory DirectoryTableNames, principal PrincipalTableNames) error {
	if err := t.bindDirectory(directory); err != nil {
		return err
	}
	return t.bindPrincipal(principal)
}

// bound reports whether both owners have published. Until they have, no
// statement that reads a table core does not own may run.
func (t *foreignTables) bound() bool {
	return t != nil && t.directory.Bound() && t.principal.Bound()
}

// authorizationTables assembles one statement's names from core's deployment
// prefix and from the two published sets.
func (t *foreignTables) authorizationTables(prefix string) (query.AuthorizationTables, error) {
	if !t.bound() {
		return query.AuthorizationTables{}, ErrUnavailable
	}
	return query.BindAuthorizationTables(prefix, t.directoryQuery(), t.principal.Principals), nil
}

// directoryNames returns the published directory names, or ErrUnavailable when
// the organization module never published them.
func (t *foreignTables) directoryNames() (query.DirectoryTables, error) {
	if t == nil || !t.directory.Bound() {
		return query.DirectoryTables{}, ErrUnavailable
	}
	return t.directoryQuery(), nil
}

// directoryQuery hands the query layer its own shape. The two structs carry the
// same twelve names: core declares one for the owner to fill, and the query
// package owns the other because a statement is assembled there.
func (t *foreignTables) directoryQuery() query.DirectoryTables {
	return query.DirectoryTables{
		Groups:               t.directory.Groups,
		GroupMembers:         t.directory.GroupMembers,
		GroupRoleBindings:    t.directory.GroupRoleBindings,
		Positions:            t.directory.Positions,
		PositionMembers:      t.directory.PositionMembers,
		PositionRoleBindings: t.directory.PositionRoleBindings,
		Departments:          t.directory.Departments,
		DepartmentClosure:    t.directory.DepartmentClosure,
		MemberDepartments:    t.directory.MemberDepartments,
		RoleDataScopes:       t.directory.RoleDataScopes,
		RoleScopeDepartments: t.directory.RoleScopeDepartments,
		Tenants:              t.directory.Tenants,
	}
}
