package core

import (
	"7link.com/platform/internal/iam/core/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// AccessTableNames is the physical name of every table core owns, as core hands them
// to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table core does not have and cannot misspell the one it wants. Every module
// that reads these is activated after core and imports it, which is what lets
// core be the side that declares the type; where the import runs the other way,
// the consumer declares the struct and the owner fills it instead.
type AccessTableNames struct {
	Entities               gormx.TableName
	FieldPolicies          gormx.TableName
	MenuContributions      gormx.TableName
	Menus                  gormx.TableName
	PlatformAdministrators gormx.TableName
	PlatformGrants         gormx.TableName
	PolicyRevisions        gormx.TableName
	Relationships          gormx.TableName
	ResourceRelationships  gormx.TableName
	Roles                  gormx.TableName
	RoleBindings           gormx.TableName
	RoleMembers            gormx.TableName
	RolePermissions        gormx.TableName
	TemporaryRoleGrants    gormx.TableName
	TenantMembers          gormx.TableName
	MembershipPeriods      gormx.TableName
}

// PublishedAccessTableNames binds every name core owns to one deployment prefix. It builds
// from the module's own BindNames, so the prefix is applied the same way it is
// everywhere else core queries, and a table added to core cannot be forgotten
// here without the drift test noticing.
func PublishedAccessTableNames(prefix string) AccessTableNames {
	names := query.BindNames(prefix)
	return AccessTableNames{
		Entities:               names.Entities,
		FieldPolicies:          names.FieldPolicies,
		MenuContributions:      names.MenuContributions,
		Menus:                  names.Menus,
		PlatformAdministrators: names.PlatformAdministrators,
		PlatformGrants:         names.PlatformGrants,
		PolicyRevisions:        names.PolicyRevisions,
		Relationships:          names.Relationships,
		ResourceRelationships:  names.ResourceRelationships,
		Roles:                  names.Roles,
		RoleBindings:           names.RoleBindings,
		RoleMembers:            names.RoleMembers,
		RolePermissions:        names.RolePermissions,
		TemporaryRoleGrants:    names.TemporaryRoleGrants,
		TenantMembers:          names.TenantMembers,
		MembershipPeriods:      names.MembershipPeriods,
	}
}

// Bound reports whether these names arrived. PublishedAccessTableNames fills every field
// at once, so one zero field means the consumer was never given the set rather
// than that core published an incomplete one.
func (t AccessTableNames) Bound() bool { return !t.TenantMembers.IsZero() }
