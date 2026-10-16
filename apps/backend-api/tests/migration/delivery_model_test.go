package migration_test

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/delivery/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestDeliveryProjectModelsAndConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			for _, persisted := range []tableModel{model.Project{}, model.ProjectContact{}, model.ProjectRepository{}, model.ProjectCounter{}, model.Requirement{}, model.AcceptanceCriterion{}, model.RequirementVersion{}} {
				assertModelMatchesNamedTable(t, db, engine.dialect, persisted, deploymentPrefix+persisted.TableName())
			}
			projects := deploymentPrefix + "delivery_projects"
			insert := func(tenant, id, code string) error {
				_, err := db.ExecContext(t.Context(), "INSERT INTO "+projects+" (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES ("+tenant+","+id+",'"+code+"','2026-10-16 00:00:00','2026-10-16 00:00:00','2026-10-16 00:00:00')")
				return err
			}
			require.NoError(t, insert("1", "2", "PR"))
			require.NoError(t, insert("2", "3", "PR"), "same code in different tenants is valid")
			require.Error(t, insert("1", "4", "PR"), "code uniqueness is tenant qualified")
			for _, code := range []string{"aB", "A", "1A", "A_B", "ABCDEFGHIJKLMNOPQ"} {
				require.Error(t, insert("1", "4", code), "invalid project code %q", code)
			}
			var stage, mode, dispatch, health, holds, portal, acceptance string
			var branches, notify, version int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT stage,team_mode,dispatch_policy,health,holds,portal_settings,acceptance_policy,ensemble_max_branches,budget_notify_pct,version FROM "+projects+" WHERE tenant_id=1 AND project_id=2").Scan(&stage, &mode, &dispatch, &health, &holds, &portal, &acceptance, &branches, &notify, &version))
			require.Equal(t, "INTAKE", stage)
			require.Equal(t, "HYBRID", mode)
			require.Equal(t, "MANUAL", dispatch)
			require.Equal(t, "GREEN", health)
			require.JSONEq(t, "[]", holds)
			require.JSONEq(t, `{"show_ai_label":true,"show_work_item_titles":true,"show_evidence_summary":true,"show_release_notes":true,"contributor_display":"TEAM_ONLY","milestones_visible":true}`, portal)
			require.JSONEq(t, `{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}`, acceptance)
			require.Equal(t, 3, branches)
			require.Equal(t, 80, notify)
			require.Equal(t, 1, version)
			for _, assignment := range []string{"target_date='not-a-date'", "target_date='2026-02-30'", "stage='UNKNOWN'", "stage='active'", "team_mode='hybrid'", "stage='BLOCKED'", "team_mode='AUTONOMOUS'", "budget_cap_micros=0", "budget_spent_micros=-1", "ensemble_max_branches=6", "budget_notify_pct=49", "archived_at='2026-10-16 00:00:00'"} {
				_, err := db.ExecContext(t.Context(), "UPDATE "+projects+" SET "+assignment+" WHERE tenant_id=1 AND project_id=2")
				require.Error(t, err, assignment)
			}
			contacts := deploymentPrefix + "delivery_project_contacts"
			contactInsert := "INSERT INTO " + contacts + " (tenant_id,project_id,intake_contact_id,role,can_accept,created_at,updated_at) VALUES "
			_, err := db.ExecContext(t.Context(), contactInsert+"(1,2,10,'PRIMARY',true,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.NoError(t, err)
			_, err = db.ExecContext(t.Context(), contactInsert+"(1,2,11,'PRIMARY',true,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.Error(t, err, "two primary contacts forbidden")
			_, err = db.ExecContext(t.Context(), contactInsert+"(2,3,12,'PRIMARY',false,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.Error(t, err, "primary must accept")
			for _, id := range []string{"11", "12"} {
				_, err = db.ExecContext(t.Context(), contactInsert+"(1,2,"+id+",'CC',false,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
				require.NoError(t, err, "multiple CC contacts allowed")
			}
			repos := deploymentPrefix + "delivery_project_repositories"
			repoInsert := "INSERT INTO " + repos + " (tenant_id,id,project_id,name,remote_url,is_primary,created_at,updated_at) VALUES "
			_, err = db.ExecContext(t.Context(), repoInsert+"(1,20,2,'source','https://example.test/repo.git',true,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.NoError(t, err)
			_, err = db.ExecContext(t.Context(), repoInsert+"(1,21,2,'other','https://example.test/other.git',true,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.Error(t, err, "two primary repositories forbidden")
			_, err = db.ExecContext(t.Context(), repoInsert+"(1,21,2,'other','https://example.test/other.git',false,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
			require.NoError(t, err)
			for column, limit := range map[string]int{"name": 100, "remote_url": 2000, "default_branch": 200} {
				_, err = db.ExecContext(t.Context(), "UPDATE "+repos+" SET "+column+"='"+strings.Repeat("x", limit+1)+"' WHERE tenant_id=1 AND id=20")
				require.Error(t, err, "%s length must match its declared storage contract", column)
			}
		})
	}
}

func TestDeliveryRequirementsConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 1))
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", deploymentPrefix))
				return err
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-16','2026-10-16','2026-10-16'),(1,3,'PS','2026-10-16','2026-10-16','2026-10-16'),(2,4,'PT','2026-10-16','2026-10-16','2026-10-16')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.Equal(t, 3, tableRowCount(t, db, deploymentPrefix+"delivery_projects"), "upgrade preserves existing projects")
			req := "INSERT INTO @delivery_requirements (tenant_id,id,project_id,number,title,created_at,updated_at) VALUES "
			require.NoError(t, exec(req+"(1,10,2,1,'Requirement','2026-10-16','2026-10-16'),(1,11,3,1,'Other project','2026-10-16','2026-10-16'),(2,12,4,1,'Other tenant','2026-10-16','2026-10-16')"))
			for _, values := range []string{"(1,13,2,1,'Duplicate','2026-10-16','2026-10-16')", "(2,13,2,2,'Foreign','2026-10-16','2026-10-16')", "(1,13,2,2,'   ','2026-10-16','2026-10-16')"} {
				require.Error(t, exec(req+values), values)
			}
			for _, assignment := range []string{"priority=-1", "priority=4", "status='draft'", "status='UNKNOWN'", "number=0", "version=0", "current_version_no=-1", "status='BASELINED'", "title='" + strings.Repeat("x", 201) + "'"} {
				require.Error(t, exec("UPDATE @delivery_requirements SET "+assignment+" WHERE tenant_id=1 AND id=10"), assignment)
			}
			require.NoError(t, exec("INSERT INTO @delivery_project_counters (tenant_id,id,project_id,created_at,updated_at) VALUES (1,20,2,'2026-10-16','2026-10-16')"))
			require.Error(t, exec("INSERT INTO @delivery_project_counters (tenant_id,id,project_id,created_at,updated_at) VALUES (1,21,2,'2026-10-16','2026-10-16')"))
			require.Error(t, exec("UPDATE @delivery_project_counters SET next_work_item_no=0 WHERE tenant_id=1 AND id=20"))
			criterion := "INSERT INTO @delivery_acceptance_criteria (tenant_id,id,project_id,requirement_id,code,text,position,created_at) VALUES "
			require.NoError(t, exec(criterion+"(1,30,2,10,'C1','Criterion',0,'2026-10-16'),(1,31,2,10,'C2','Replacement',1,'2026-10-16')"))
			for _, values := range []string{"(1,32,3,10,'C3','Wrong project',2,'2026-10-16')", "(2,32,4,10,'C3','Wrong tenant',2,'2026-10-16')", "(1,32,2,10,'C1','Duplicate',2,'2026-10-16')", "(1,32,2,10,'C3','Bad position',50,'2026-10-16')"} {
				require.Error(t, exec(criterion+values), values)
			}
			require.NoError(t, exec("UPDATE @delivery_acceptance_criteria SET supersedes_id=30,introduced_in_version=2 WHERE tenant_id=1 AND id=31"))
			require.Error(t, exec("UPDATE @delivery_acceptance_criteria SET retired_in_version=1 WHERE tenant_id=1 AND id=31"))
			require.Error(t, exec("UPDATE @delivery_acceptance_criteria SET supersedes_id=999 WHERE tenant_id=1 AND id=31"))
			version := "INSERT INTO @delivery_requirement_versions (tenant_id,id,project_id,requirement_id,version_no,title,description,priority,criteria,content_hash,created_at) VALUES "
			hash := strings.Repeat("a", 64)
			require.NoError(t, exec(version+"(1,40,2,10,1,'Baseline','',2,'[]','"+hash+"','2026-10-16')"))
			require.Error(t, exec(version+"(1,41,2,10,1,'Duplicate','',2,'[]','"+hash+"','2026-10-16')"))
			require.Error(t, exec(version+"(1,41,3,10,1,'Wrong project','',2,'[]','"+hash+"','2026-10-16')"))
			require.Error(t, exec(version+"(1,41,2,10,2,'Missing change','',2,'[]','"+hash+"','2026-10-16')"))
			require.Error(t, exec("UPDATE @delivery_requirement_versions SET criteria='invalid' WHERE tenant_id=1 AND id=40"))
			require.Error(t, exec("UPDATE @delivery_requirement_versions SET criteria='{}' WHERE tenant_id=1 AND id=40"))
			require.Error(t, exec("DELETE FROM @delivery_requirements WHERE tenant_id=1 AND id=10"))
			require.Error(t, exec("DELETE FROM @delivery_projects WHERE tenant_id=1 AND project_id=2"))
			require.NoError(t, exec(req+"(1,14,2,2,'Second requirement','2026-10-16','2026-10-16')"))
			require.NoError(t, exec(criterion+"(1,32,2,14,'C1','Other requirement',0,'2026-10-16')"))
			require.Error(t, exec("UPDATE @delivery_acceptance_criteria SET supersedes_id=32 WHERE tenant_id=1 AND id=31"), "supersession cannot cross requirements even in the same project")
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 1))
			require.Equal(t, 3, tableRowCount(t, db, deploymentPrefix+"delivery_projects"), "rollback of new tables preserves projects")
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.Equal(t, 3, tableRowCount(t, db, deploymentPrefix+"delivery_projects"))
			require.Zero(t, tableRowCount(t, db, deploymentPrefix+"delivery_requirements"))

		})
	}
}
