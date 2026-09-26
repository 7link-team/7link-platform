package migration_test

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"7link.com/platform/internal/app"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproduce the deployed schema without changing historical migration files.
type beforePositionRetirement struct{ module.Module }

func (m beforePositionRetirement) MigrationSource() goosex.Source {
	source := m.Module.(module.MigrationSourceProvider).MigrationSource()
	files := fstest.MapFS{}
	err := fs.WalkDir(source.FS, "sql", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.HasSuffix(path, "_retire_positions.sql") {
			data, readErr := fs.ReadFile(source.FS, path)
			if readErr != nil {
				return readErr
			}
			files[path] = &fstest.MapFile{Data: data}
		}
		return nil
	})
	if err != nil { panic(err) }
	source.FS = files
	return source
}

func (m beforePositionRetirement) MigrationMilestones() []module.MigrationMilestone {
	all := m.Module.(module.MigrationMilestoneProvider).MigrationMilestones()
	if all[len(all)-1].Name == "retire-positions" {
		return all[:len(all)-1]
	}
	return all
}

func TestIAMPositionRetirementUpgrade(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					if engine.name == "mysql8" {
						prefix = "retire_" + prefix
					}
					db := engine.open(t)
					orm := openMilestoneGORM(t, db, engine.dialect)
					modules := []module.Module{&governance.Module{}, &iamcore.Module{}, &organization.Module{}, &identity.Module{}}
					old := make([]module.Module, len(modules))
					for i, m := range modules {
						old[i] = beforePositionRetirement{m}
					}
					_, err := app.ExecuteModuleMigrations(t.Context(), orm, old, app.MigrationRequest{Action: app.MigrationUp}, prefix)
					require.NoError(t, err)
					seedOrganizationParents(t, db, prefix)
					exec := func(query string) {
						t.Helper()
						_, err := db.ExecContext(t.Context(), strings.ReplaceAll(query, "@", prefix))
						require.NoError(t, err)
					}
					exec("INSERT INTO @iam_positions (tenant_id,id,code,name,status,sort_order,version,created_at,updated_at) VALUES (1,301,'operator','Operator','active',7,3,'2026-09-02 00:00:00','2026-09-02 00:00:00'),(1,302,'disabled','Disabled','disabled',8,2,'2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_position_members (tenant_id,position_id,principal_id,starts_at,ends_at,created_at,updated_at) VALUES (1,301,20,'2026-09-01 00:00:00','2026-10-01 00:00:00','2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_position_role_bindings (tenant_id,position_id,role_id,created_at) VALUES (1,301,10,'2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_role_permissions (tenant_id,role_id,permission_code,created_at) VALUES (1,10,'position_view','2026-09-02 00:00:00'),(1,10,'dept_view','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_entities (tenant_id,id,type,name,name_key,status,metadata,created_at,updated_at) VALUES (1,400,'store','Store','store','active','{}','2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_relationships (tenant_id,subject_type,subject_id,subject_relation,relation,resource_type,resource_id,created_at) VALUES (1,'position',301,'member','viewer','store',400,'2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_resource_relationships (tenant_id,entity_id,subject_type,subject_id,relation,resource_type,resource_id,created_at) VALUES (1,400,'position',301,'viewer','store',400,'2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_menus (tenant_id,id,label,route,permission_code,status,created_at,updated_at) VALUES (1,500,'Positions','/positions','position_view','active','2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_menus (tenant_id,id,parent_id,label,route,status,created_at,updated_at) VALUES (1,501,500,'Custom','/custom','active','2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_access_reviews (tenant_id,id,name,owner_id,status,due_at,created_at,updated_at) VALUES (1,600,'Review',20,'open','2026-10-01 00:00:00','2026-09-02 00:00:00','2026-09-02 00:00:00')")
					exec("INSERT INTO @iam_access_review_items (tenant_id,review_id,id,principal_id,principal_name,role_id,role_name,grant_type,grant_id,grant_created_at,decision) VALUES (1,600,601,20,'Alice',10,'Operator','position',301,'2026-09-02 00:00:00','pending')")
					// Conflicting IDs must fail without overwriting or deleting either source.
					exec("INSERT INTO @iam_groups (tenant_id,id,name,name_key,group_type,status,version,created_at,updated_at) VALUES (1,301,'Existing','existing','static','active',1,'2026-09-02 00:00:00','2026-09-02 00:00:00')")
					_, err = app.ExecuteModuleMigrations(t.Context(), orm, modules, app.MigrationRequest{Action: app.MigrationUp}, prefix)
					require.Error(t, err)
					assert.EqualValues(t, 2, tableRowCount(t, db, prefix+"iam_positions"))
					assert.EqualValues(t, 1, tableRowCount(t, db, prefix+"iam_groups"))
					exec("DELETE FROM @iam_groups WHERE id=301")
					_, err = app.ExecuteModuleMigrations(t.Context(), orm, modules, app.MigrationRequest{Action: app.MigrationUp}, prefix)
					require.NoError(t, err)
					for _, table := range []string{"iam_positions", "iam_position_members", "iam_position_role_bindings"} {
						assert.Zero(t, tableRowCount(t, db, prefix+table))
					}
					var name, status, kind string
					var order, version int
					require.NoError(t, db.QueryRowContext(t.Context(), "SELECT name,status,group_type,sort_order,version FROM "+prefix+"iam_groups WHERE id=301").Scan(&name, &status, &kind, &order, &version))
					assert.Equal(t, "Group 301: Operator", name)
					assert.Equal(t, "active", status)
					assert.Equal(t, "static", kind)
					assert.Equal(t, 7, order)
					assert.Equal(t, 3, version)
					checks := map[string]int{
						"iam_groups WHERE id=302 AND status='disabled'": 1,
						"iam_group_members WHERE group_id=301 AND principal_id=20 AND starts_at='2026-09-01 00:00:00' AND ends_at='2026-10-01 00:00:00'": 1,
						"iam_group_role_bindings WHERE group_id=301 AND role_id=10":                                                                      1,
						"iam_relationships WHERE subject_type='group' AND subject_id=301":                                                                1,
						"iam_resource_relationships WHERE subject_type='group' AND subject_id=301":                                                       1,
						"iam_access_review_items WHERE grant_type='group' AND grant_id=301 AND decision='pending'":                                       1,
						"iam_menus WHERE id=501 AND parent_id IS NULL":                                                                                   1,
						"iam_menus WHERE id=500": 0,
						"iam_role_permissions WHERE permission_code='position_view' OR permission_code LIKE 'group_%'": 0,
						"iam_role_permissions WHERE permission_code='dept_view'":                                       1,
					}
					for query, want := range checks {
						var count int
						require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT COUNT(*) FROM %s%s", prefix, query)).Scan(&count))
						assert.Equal(t, want, count, query)
					}
					_, err = app.ExecuteModuleMigrations(t.Context(), orm, modules, app.MigrationRequest{Action: app.MigrationUp}, prefix)
					require.NoError(t, err, "repeat upgrades must be idempotent")
				})
			}
		})
	}
}
