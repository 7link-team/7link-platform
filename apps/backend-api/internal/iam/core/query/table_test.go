package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func dryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		DryRun: true,
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return db
}

func TestBindAppliesPrefixToEveryCoreTable(t *testing.T) {
	t.Parallel()

	db := dryRunDB(t)
	plain := Bind(db, "")
	prefixed := Bind(db, "sl_")
	actual := map[string][2]string{
		"entities":                {plain.Entities.TableName(), prefixed.Entities.TableName()},
		"menus":                   {plain.Menus.TableName(), prefixed.Menus.TableName()},
		"platform_administrators": {plain.PlatformAdministrators.TableName(), prefixed.PlatformAdministrators.TableName()},
		"platform_grants":         {plain.PlatformGrants.TableName(), prefixed.PlatformGrants.TableName()},
		"policy_revisions":        {plain.PolicyRevisions.TableName(), prefixed.PolicyRevisions.TableName()},
		"relationships":           {plain.Relationships.TableName(), prefixed.Relationships.TableName()},
		"resource_relationships":  {plain.ResourceRelationships.TableName(), prefixed.ResourceRelationships.TableName()},
		"roles":                   {plain.Roles.TableName(), prefixed.Roles.TableName()},
		"role_bindings":           {plain.RoleBindings.TableName(), prefixed.RoleBindings.TableName()},
		"role_members":            {plain.RoleMembers.TableName(), prefixed.RoleMembers.TableName()},
		"role_permissions":        {plain.RolePermissions.TableName(), prefixed.RolePermissions.TableName()},
		"temporary_role_grants":   {plain.TemporaryRoleGrants.TableName(), prefixed.TemporaryRoleGrants.TableName()},
		"tenant_members":          {plain.TenantMembers.TableName(), prefixed.TenantMembers.TableName()},
	}

	require.Len(t, actual, 13)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
