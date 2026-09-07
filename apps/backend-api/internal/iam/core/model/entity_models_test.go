package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface {
	TableName() string
}

func TestCoreModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()

	models := []tableNamer{
		Role{}, RolePermission{}, RoleMember{}, TenantMember{}, Menu{}, Entity{},
		RoleBinding{}, PolicyRevision{}, PlatformAdministrator{}, Relationship{},
		ResourceRelationship{}, TemporaryRoleGrant{}, PlatformGrant{}, FieldPolicy{},
	}
	expected := []string{
		"iam_roles", "iam_role_permissions", "iam_role_members", "iam_tenant_members",
		"iam_menus", "iam_entities", "iam_role_bindings", "iam_policy_revisions",
		"iam_platform_administrators", "iam_relationships", "iam_resource_relationships",
		"iam_temporary_role_grants", "iam_platform_grants", "iam_field_policies",
	}

	require.Len(t, models, 14)
	seen := make(map[string]struct{}, len(models))
	for index, item := range models {
		name := item.TableName()
		assert.Equal(t, expected[index], name)
		_, duplicate := seen[name]
		assert.False(t, duplicate, "duplicate model table %s", name)
		seen[name] = struct{}{}
	}
}
