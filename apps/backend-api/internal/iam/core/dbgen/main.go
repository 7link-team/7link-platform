// Command dbgen generates core IAM's typed query package.
package main

import (
	"7link.com/platform/internal/iam/core/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(
		model.Role{},
		model.RolePermission{},
		model.FieldPolicy{},
		model.RoleMember{},
		model.TenantMember{},
		model.MembershipPeriod{},
		model.Menu{},
		model.MenuContribution{},
		model.Entity{},
		model.RoleBinding{},
		model.PolicyRevision{},
		model.PlatformAdministrator{},
		model.Relationship{},
		model.ResourceRelationship{},
		model.TemporaryRoleGrant{},
		model.PlatformGrant{},
	)
}
