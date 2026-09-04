package migration_test

import (
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMOrganizationModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{
		model.Department{}, model.DepartmentClosure{}, model.Position{}, model.PositionMember{},
		model.Group{}, model.GroupMember{}, model.GroupRoleBinding{}, model.PositionRoleBinding{},
		model.Tenant{}, model.MemberDepartment{}, model.RoleDataScope{}, model.RoleScopeDepartment{},
		model.Invitation{}, model.InvitationRole{},
	}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	require.Len(t, models, 14)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gormDB := openMilestoneGORM(t, db, engine.dialect)
			_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, organizationMigrationModules(), app.MigrationRequest{Action: app.MigrationUp}, "")
			require.NoError(t, err)
			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) { assertModelMatchesTable(t, db, engine.dialect, persisted) })
			}
		})
	}
}
