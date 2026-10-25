package migration_test

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestDeliveryDefectDeploymentLifecycleUpgradeAndRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "defect_lifecycle_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 37))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Bug','BUG',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_result_versions (tenant_id,id,project_id,work_item_id,sequence,summary,actor_id,baseline_version_no,created_at) VALUES (1,30,2,20,1,'Output',100,0,'2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 38))
			assertModelMatchesNamedTable(t, db, engine.dialect, model.Deployment{}, source.Prefix+model.Deployment{}.TableName())
			insert := "INSERT INTO @delivery_deployments (tenant_id,id,project_id,work_item_id,result_id,producer_id,event_key,environment,commit_hash,artifact_hash,merge_receipt_hash,fixed_version,payload_hash,occurred_at,created_at) VALUES (1,40,2,20,30,100,'pipeline-1','staging','" + strings.Repeat("a", 40) + "','" + strings.Repeat("b", 64) + "','" + strings.Repeat("c", 64) + "','v2','" + strings.Repeat("d", 64) + "','2040-01-01','2040-01-01')"
			require.NoError(t, exec(insert))
			require.Error(t, exec(strings.Replace(insert, "(1,40,", "(1,41,", 1)), "producer/event uniqueness must survive retry")
			if engine.name != "mysql" {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 37), "never drop immutable deployment facts")
				require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_deployments"))
			}
			require.NoError(t, exec("DELETE FROM @delivery_deployments"))
			require.NoError(t, exec("UPDATE @delivery_work_items SET reopened_from_work_item_id=20 WHERE id=20"))
			if engine.name != "mysql" {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 37), "reopen lineage also prevents downgrade")
			}
			require.NoError(t, exec("UPDATE @delivery_work_items SET reopened_from_work_item_id=NULL WHERE id=20"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 37))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 38))
			require.NoError(t, exec(insert))
		})
	}
}

func TestDeliveryDisplayTransitionsUpgradeAndRetainedHistoryAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "display_upgrade_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 33))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_item_transitions (tenant_id,id,project_id,work_item_id,from_state,to_state,trigger_kind,actor_kind,actor_id,authority_source,reason_code,source_ref,version_after,created_at) VALUES (1,30,2,20,'DRAFT','READY','COMMAND','HUMAN',100,'permission:work_item_write','READINESS_CHECKED','readiness-check:40',2,'2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 34))
			var from, to, comment sql.NullString
			read := func() {
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT from_state_key,to_state_key,comment FROM "+source.Prefix+"delivery_work_item_transitions WHERE id=30").Scan(&from, &to, &comment))
			}
			read()
			require.False(t, from.Valid)
			require.False(t, to.Valid)
			require.False(t, comment.Valid)
			require.NoError(t, exec("UPDATE @delivery_work_item_transitions SET from_state_key='draft',to_state_key='scheduled',comment='Reviewed source facts' WHERE id=30"))
			if engine.name != "mysql" { // Schema harness does not enforce added MySQL CHECKs.
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 33))
				read()
				require.Equal(t, "scheduled", to.String)
				require.Equal(t, "Reviewed source facts", comment.String)
			}
			require.NoError(t, exec("UPDATE @delivery_work_item_transitions SET from_state_key=NULL,to_state_key=NULL,comment=NULL WHERE id=30"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 33))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 34))
			read()
			require.False(t, to.Valid)
		})
	}
}

func TestDeliveryDefectDetailsUpgradeAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "defect_upgrade_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 32))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage,constitution_version_no,autonomy_version,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','ACTIVE',3,4,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Bug','BUG',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 33))
			var details sql.NullString
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT defect_details FROM "+source.Prefix+"delivery_work_items WHERE id=20").Scan(&details))
			require.False(t, details.Valid)
			require.NoError(t, exec(`UPDATE @delivery_work_items SET defect_details='{"steps":"Open","expected":"Loads","actual":"Fails"}' WHERE id=20`))
			if engine.name != "mysql" { // The schema harness does not enforce added MySQL CHECK constraints.
				require.Error(t, exec("UPDATE @delivery_work_items SET defect_details='invalid-json' WHERE id=20"))
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 32))
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT defect_details FROM "+source.Prefix+"delivery_work_items WHERE id=20").Scan(&details))
				require.True(t, details.Valid)
			}
			require.NoError(t, exec("UPDATE @delivery_work_items SET defect_details=NULL WHERE id=20"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 32))
			require.Error(t, exec("UPDATE @delivery_work_items SET defect_details=NULL WHERE id=20"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 33))
		})
	}
}

func TestDeliveryAssignmentsAndStagesConstraintsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "lease_stage_"
			exec := func(statement string) error {
				_, e := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return e
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 6))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 6))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			assignment := "INSERT INTO @delivery_assignments (tenant_id,id,project_id,work_item_id,role,assignee_kind,assignee_principal_id,status,active_slot,lease_ttl_ms,version,created_at,updated_at) VALUES "
			require.NoError(t, exec(assignment+"(1,30,2,20,'EXECUTOR','HUMAN',100,'ACTIVE',20,14400000,1,'2026-10-18','2026-10-18')"))
			require.Error(t, exec(assignment+"(1,31,2,20,'EXECUTOR','HUMAN',101,'ACTIVE',20,14400000,1,'2026-10-18','2026-10-18')"), "one active executor independent of command CAS")
			for _, change := range []string{"active_slot=NULL", "active_slot=21", "role='UNKNOWN'", "status='EXPIRED'", "version=0", "lease_ttl_ms=0", "agent_id=9", "project_id=9"} {
				require.Error(t, exec("UPDATE @delivery_assignments SET "+change+" WHERE id=30"), change)
			}
			require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 6))
			require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_assignments"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			stage := "INSERT INTO @delivery_project_stage_transitions (tenant_id,id,project_id,from_stage,to_stage,actor_kind,actor_id,authority_source,reason,criteria_snapshot,version_after,occurred_at) VALUES "
			require.NoError(t, exec(stage+"(1,40,2,'INTAKE','DISCOVERY','HUMAN',100,'ROLE:delivery_lead','','[]',2,'2026-10-18')"))
			for _, change := range []string{"from_stage='DISCOVERY'", "actor_kind='UNKNOWN'", "version_after=1", "project_id=999"} {
				require.Error(t, exec("UPDATE @delivery_project_stage_transitions SET "+change+" WHERE id=40"), change)
			}
			require.Error(t, exec(stage+"(1,41,2,'INTAKE','DISCOVERY','HUMAN',100,'ROLE:delivery_lead','','[]',2,'2026-10-18')"), "one immutable transition per resulting project version")
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 7))
				require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_project_stage_transitions"))
			}
		})
	}
}

func TestDeliveryCommentDefectConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "comment_defect_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2040-01-01','2040-01-01','2040-01-01')"))
			for _, work := range []int{20, 21, 22} {
				require.NoError(t, exec(fmt.Sprintf("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,%d,2,%d,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')", work, work)))
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			row := model.CommentDefect{ID: 30, TenantID: 1, ProjectID: 2, WorkItemID: 21, ThreadID: 40, ExecutionID: 50, SourceFirstBody: "Original comment", FoundInWorkItemID: 20, AnchorSnapshot: `{}`, Steps: "Frozen steps", Expected: "Expected", Actual: "Actual override", FoundStage: "IN_PROGRESS", CreatedBy: 100, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.CommentDefect
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).First(&stored).Error)
			require.Equal(t, row.SourceFirstBody, stored.SourceFirstBody)
			require.Equal(t, row.Actual, stored.Actual)
			require.Equal(t, row.ExecutionID, stored.ExecutionID)
			duplicate := row
			duplicate.ID, duplicate.WorkItemID = 31, 22
			require.Error(t, db.Table(table).Create(&duplicate).Error, "a thread has one defect")
			duplicate.ThreadID, duplicate.WorkItemID = 41, 21
			require.Error(t, db.Table(table).Create(&duplicate).Error, "a defect has one frozen source")
			for field, value := range map[string]any{"tenant_id": 999, "project_id": 999, "work_item_id": 999, "found_in_work_item_id": 999} {
				require.Error(t, db.Table(table).Where("id = ?", row.ID).Update(field, value).Error, field)
			}
			require.Error(t, exec("DELETE FROM @delivery_work_items WHERE id=20"), "found-in source is retained")
			require.Error(t, exec("DELETE FROM @delivery_work_items WHERE id=21"), "defect target is retained")
		})
	}
}

func TestDeliveryConstitutionsUpgradeAndRefusedRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "constitution_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 5))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18'),(1,3,'PS','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			var invented int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM constitution_delivery_projects WHERE constitution_version_no IS NOT NULL").Scan(&invented))
			require.Zero(t, invented)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 5))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			insert := "INSERT INTO @delivery_project_constitutions (tenant_id,id,project_id,version_no,body,body_sha256,state,change_summary,proposed_by_kind,proposed_by,created_at,updated_at,version) VALUES "
			require.NoError(t, exec(insert+"(1,10,2,1,'Rules','"+strings.Repeat("a", 64)+"','DRAFT','Initial','HUMAN',100,'2026-10-18','2026-10-18',1),(1,11,2,2,'Rules','"+strings.Repeat("a", 64)+"','DRAFT','Second','HUMAN',100,'2026-10-18','2026-10-18',1)"))
			for _, assignment := range []string{"state='ACTIVE'", "active_key=2", "state='REJECTED',reviewed_by=100,reviewed_at='2026-10-18'", "project_id=999", "version=0", "version_no=0", "body=''", "body_sha256='short'", "state='unknown'", "proposed_by_kind='unknown'"} {
				require.Error(t, exec("UPDATE @delivery_project_constitutions SET "+assignment+" WHERE id=10"), assignment)
			}
			for _, fixture := range []struct {
				body  string
				valid bool
			}{{strings.Repeat("x", 65536), true}, {strings.Repeat("x", 65537), false}, {strings.Repeat("界", 21845) + "x", true}, {strings.Repeat("界", 21846), false}} {
				err := exec("UPDATE @delivery_project_constitutions SET body='" + fixture.body + "' WHERE id=10")
				if fixture.valid {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
			require.NoError(t, exec("UPDATE @delivery_project_constitutions SET change_summary='"+strings.Repeat("界", 2000)+"',review_comment='"+strings.Repeat("界", 2000)+"' WHERE id=10"))
			require.NoError(t, exec("UPDATE @delivery_project_constitutions SET state='ACTIVE',active_key=2,reviewed_by=100,reviewed_at='2026-10-18' WHERE id=10"))
			require.Error(t, exec("UPDATE @delivery_project_constitutions SET state='ACTIVE',active_key=2,reviewed_by=100,reviewed_at='2026-10-18' WHERE id=11"), "database must reject second active version")
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 5))
				require.Equal(t, 2, tableRowCount(t, db, "constitution_delivery_project_constitutions"))
			}
			require.NoError(t, exec("DELETE FROM @delivery_project_constitutions"))
			require.NoError(t, exec("UPDATE @delivery_projects SET constitution_version_no=1 WHERE project_id=2"))
			require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 5))
			require.NoError(t, exec("UPDATE @delivery_projects SET constitution_version_no=NULL"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 5))
			require.Equal(t, 2, tableRowCount(t, db, "constitution_delivery_projects"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
		})
	}
}

// This tests storage invariants only. It does not bypass the still-pending
// work-item commands, decisions, delivery assembly or sending workflows.
func TestDeliveryReadinessUpgradeAndRefusedRollbackAcrossEngines(t *testing.T) {
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
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "readiness_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 4))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18'),(1,3,'PS','2026-10-18','2026-10-18','2026-10-18'),(2,2,'PT','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18'),(1,21,2,2,'Before','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18'),(1,22,3,1,'Other project','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18'),(2,23,2,1,'Other tenant','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			var confirmed int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM readiness_delivery_work_items WHERE out_of_scope_confirmed_at IS NOT NULL").Scan(&confirmed))
			require.Zero(t, confirmed, "upgrade must not invent confirmation")
			dependency := "INSERT INTO @delivery_work_item_dependencies (tenant_id,project_id,work_item_id,depends_on_id) VALUES "
			for _, values := range []string{"(1,2,20,20)", "(1,2,20,22)", "(1,2,20,23)", "(1,2,20,999)"} {
				require.Error(t, exec(dependency+values), values)
			}
			triggerColumn := "trigger_kind"
			checkInsert := "INSERT INTO @delivery_readiness_checks (tenant_id,id,project_id,work_item_id,work_item_version," + triggerColumn + ",ok,issues,checked_by,created_at) VALUES (1,30,2,20,2,'EDIT',true,'[]','SYSTEM','2026-10-18')"
			transitionInsert := "INSERT INTO @delivery_work_item_transitions (tenant_id,id,project_id,work_item_id,from_state,to_state," + triggerColumn + ",actor_kind,actor_id,authority_source,reason_code,version_after,created_at) VALUES (1,40,2,20,'DRAFT','READY','COMMAND','HUMAN',100,'work_item_write','READINESS_CHECK',2,'2026-10-18')"
			for _, fixture := range []struct{ setup, cleanup string }{
				{"UPDATE @delivery_work_items SET out_of_scope_confirmed_at='2026-10-18' WHERE tenant_id=1 AND id=20", "UPDATE @delivery_work_items SET out_of_scope_confirmed_at=NULL"},
				{dependency + "(1,2,20,21)", "DELETE FROM @delivery_work_item_dependencies"},
				{checkInsert, "DELETE FROM @delivery_readiness_checks"},
				{transitionInsert, "DELETE FROM @delivery_work_item_transitions"},
			} {
				require.NoError(t, exec(fixture.setup), fixture.setup)
				for retry := 0; retry < 2; retry++ {
					require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 4))
					require.Equal(t, 4, tableRowCount(t, db, "readiness_delivery_work_items"))
				}
				require.NoError(t, exec(fixture.cleanup))
			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 4))
			require.Equal(t, 4, tableRowCount(t, db, "readiness_delivery_work_items"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
		})
	}
}

func TestDeliveryWorkContractsUpgradeAndRefusedRollbackRecoverAcrossEngines(t *testing.T) {
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
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "contract_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 3))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_requirements (tenant_id,id,project_id,number,title,created_at,updated_at) VALUES (1,10,2,1,'Existing','2026-10-18','2026-10-18')"))
			insert := "INSERT INTO @delivery_work_items (tenant_id,id,project_id,requirement_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES "
			require.NoError(t, exec(insert+"(1,20,2,10,1,'Parent','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18'),(1,21,2,10,2,'Child','BUG',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("UPDATE @delivery_work_items SET parent_id=20 WHERE id=21"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.Equal(t, 2, tableRowCount(t, db, "contract_delivery_work_items"))
			var parent int64
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT parent_id FROM contract_delivery_work_items WHERE id=21").Scan(&parent))
			require.Equal(t, int64(20), parent)
			require.NoError(t, exec(insert+"(1,22,2,NULL,3,'Independent','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_item_criteria (tenant_id,id,project_id,work_item_id,position,text,verification_method,created_at,created_by) VALUES (1,30,2,22,0,'Clause','Verify','2026-10-18',100)"))
			require.NoError(t, exec("INSERT INTO @delivery_work_item_contracts (tenant_id,id,project_id,work_item_id,version_no,snapshot,content_hash,confirmed_at,confirmed_by) VALUES (1,40,2,22,1,'{}','"+strings.Repeat("a", 64)+"','2026-10-18',100)"))
			for i := 0; i < 2; i++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 3))
				require.Equal(t, 3, tableRowCount(t, db, "contract_delivery_work_items"))
				require.Equal(t, 1, tableRowCount(t, db, "contract_delivery_work_item_contracts"))
			}
			if engine.dialect == "sqlite" {
				var on int
				require.NoError(t, db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on))
				require.Equal(t, 1, on)
				require.Error(t, exec("UPDATE @delivery_work_items SET parent_id=999 WHERE id=21"))
			}
			require.NoError(t, exec("DELETE FROM @delivery_work_item_contracts"))
			require.NoError(t, exec("DELETE FROM @delivery_work_item_criteria"))
			require.NoError(t, exec("DELETE FROM @delivery_work_items WHERE id=22"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 3))
			require.Error(t, exec(insert+"(1,22,2,NULL,3,'Independent','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.Equal(t, 2, tableRowCount(t, db, "contract_delivery_work_items"))
		})
	}
}

func TestDeliveryActivityStorageUpgradeAndConstraintsAcrossEngines(t *testing.T) {
	const storagePrefix = "activity_"
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
			db := engine.open(t)
			if engine.name == "mysql" {
				// Match the real MySQL gate's database collation. The in-process
				// engine otherwise defaults to binary and hides missing enum collation.
				_, err := db.ExecContext(t.Context(), "ALTER DATABASE integration COLLATE utf8mb4_0900_ai_ci")
				require.NoError(t, err)
			}
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = storagePrefix
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", storagePrefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 2))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18'),(1,3,'PS','2026-10-18','2026-10-18','2026-10-18'),(2,2,'PT','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_requirements (tenant_id,id,project_id,number,title,created_at,updated_at) VALUES (1,10,2,1,'Existing requirement','2026-10-18','2026-10-18'),(1,11,3,1,'Other project','2026-10-18','2026-10-18'),(2,10,2,1,'Other tenant','2026-10-18','2026-10-18'),(2,12,2,2,'Tenant-only requirement','2026-10-18','2026-10-18')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			workItem := "INSERT INTO @delivery_work_items (tenant_id,id,project_id,requirement_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES "
			for _, values := range []string{
				"(1,20,2,10,1,'Work item','FEATURE',0,100,'2026-10-18','2026-10-18','2026-10-18')",
				"(1,21,2,10,2,'Child','BUG',0,100,'2026-10-18','2026-10-18','2026-10-18')",
				"(1,22,3,11,1,'Other project','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')",
				"(2,20,2,10,1,'Other tenant','CONTENT',0,200,'2026-10-18','2026-10-18','2026-10-18')",
				"(2,24,2,12,2,'Tenant-only item','TASK',0,200,'2026-10-18','2026-10-18','2026-10-18')",
			} {
				require.NoError(t, exec(workItem+values))
			}
			// Same resource IDs and project numbers are valid across tenants.
			require.Equal(t, 5, tableRowCount(t, db, storagePrefix+"delivery_work_items"))
			require.Error(t, exec(workItem+"(1,23,2,10,1,'Duplicate','TASK',0,100,'2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("UPDATE @delivery_work_items SET parent_id=20 WHERE tenant_id=1 AND id=21"))
			for _, assignment := range []string{
				"project_id=999", "requirement_id=11", "requirement_id=12", "requirement_id=999", "parent_id=22", "parent_id=24", "parent_id=999", "parent_id=id",
				"number=0", "title=' '", "type='INVALID'", "risk='INVALID'", "state='INVALID'", "autonomy_level='INVALID'", "required_runtime='INVALID'",
				"pause_reason='INVALID'", "escalation_reason='INVALID'", "stopped_from_state='INVALID'", "severity='INVALID'", "priority='INVALID'", "reproducibility='INVALID'", "defect_stage='INVALID'",
				"estimate_minutes=14", "estimate_minutes=4801", "self_confidence=-1", "self_confidence=101", "baseline_version_no=-1", "baseline_version_no=201", "baseline_stale_since=0", "baseline_stale_since=201",
				"quality_rework_count=-1", "infra_retry_count=-1", "internal_round_count=-1", "escape_count=-1", "activity_seq=-1", "version=0", "archived_at='2026-10-18'",
			} {
				t.Run("work_item/"+assignment, func(t *testing.T) {
					require.Error(t, exec("UPDATE @delivery_work_items SET "+assignment+" WHERE tenant_id=1 AND id=20"))
				})
			}
			for column, values := range map[string][]string{
				"type": {"FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"},
				"risk": {"LOW", "MEDIUM", "HIGH"}, "autonomy_level": {"LOW", "MEDIUM", "HIGH"},
				"state":              {"DRAFT", "READY", "CLAIMED", "IN_PROGRESS", "PLAN_REVIEW", "AGENT_CLAIMED_DONE", "VERIFYING", "INTERNAL_QA", "NEEDS_HUMAN_REVIEW", "ACCEPTED", "REWORK", "BLOCKED", "EXECUTION_FAILED", "STOPPED", "EXCEPTION", "SKIPPED", "CANCELLED"}, //nolint:misspell // Spec 00 freezes CANCELLED.
				"stopped_from_state": {"READY", "IN_PROGRESS", "PLAN_REVIEW"},
				"pause_reason":       {"MANUAL", "BUDGET", "AUTONOMY_REWORK_LIMIT", "AUTONOMY_SPINNING", "ESCALATION_RULE"}, "required_runtime": {"CLAUDE_CODE", "CODEX"},
				"escalation_reason": {"INTERNAL_ROUNDS_EXHAUSTED", "REQUIREMENT_CONFLICT", "ANDON", "TOLERANCE_EXCEPTION", "BUDGET", "MANDATORY_HUMAN_GATE"},
				"severity":          {"FATAL", "CRITICAL", "NORMAL", "MINOR"}, "priority": {"URGENT", "HIGH", "MEDIUM", "LOW"}, "reproducibility": {"ALWAYS", "HIGH", "MEDIUM", "LOW", "UNABLE"}, "defect_stage": {"MERGED", "DEPLOYED", "VERIFIED", "CLOSED"},
			} {
				for _, value := range values {
					t.Run("enum_case/"+column+"/"+value, func(t *testing.T) {
						require.NoError(t, exec("UPDATE @delivery_work_items SET "+column+"='"+value+"' WHERE tenant_id=1 AND id=20"))
						require.Error(t, exec("UPDATE @delivery_work_items SET "+column+"='"+strings.ToLower(value)+"' WHERE tenant_id=1 AND id=20"))
					})
				}
			}
			// Every JSON collection rejects both malformed JSON and the wrong shape.
			for _, column := range []string{"block_reasons", "skill_tags", "preferred_skill_tags", "out_of_scope", "context_refs", "verification_plan", "readiness_issues"} {
				for _, invalid := range []string{"invalid", "{}", "null"} {
					t.Run(column+"/"+invalid, func(t *testing.T) {
						require.Error(t, exec("UPDATE @delivery_work_items SET "+column+"='"+invalid+"' WHERE tenant_id=1 AND id=20"))
					})
				}
			}
			for _, column := range []string{"budget", "tolerance"} {
				require.Error(t, exec("UPDATE @delivery_work_items SET "+column+"='[]' WHERE tenant_id=1 AND id=20"))
			}
			for column, limit := range map[string]int{"skill_tags": 20, "preferred_skill_tags": 20, "out_of_scope": 20, "context_refs": 50} {
				value := "[" + strings.Repeat("{},", limit) + "{}]"
				require.Error(t, exec("UPDATE @delivery_work_items SET "+column+"='"+value+"' WHERE tenant_id=1 AND id=20"), column)
			}
			for _, assignment := range []string{"estimate_minutes=15", "estimate_minutes=4800", "self_confidence=0", "self_confidence=100", "baseline_version_no=200", "activity_seq=9007199254740993"} {
				require.NoError(t, exec("UPDATE @delivery_work_items SET "+assignment+" WHERE tenant_id=1 AND id=20"))
			}
			var activitySeq int64
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT activity_seq FROM "+storagePrefix+"delivery_work_items WHERE tenant_id=1 AND id=20").Scan(&activitySeq))
			require.Equal(t, int64(9007199254740993), activitySeq)
			var firstPass, pausedAt any
			var budgetAbsent, internalQA, readiness bool
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT first_pass,paused_at,budget IS NULL,internal_qa_required,readiness_ok FROM "+storagePrefix+"delivery_work_items WHERE tenant_id=1 AND id=20").Scan(&firstPass, &pausedAt, &budgetAbsent, &internalQA, &readiness))
			require.Nil(t, firstPass)
			require.Nil(t, pausedAt)
			require.True(t, budgetAbsent)
			require.True(t, internalQA)
			require.False(t, readiness)
			insertDelivery := "INSERT INTO @delivery_deliveries (tenant_id,id,project_id,sequence_no,number,title,created_at,updated_at) VALUES "
			for _, values := range []string{
				"(1,30,2,1,'D-PR-001','Delivery','2026-10-18','2026-10-18')",
				"(1,31,2,2,'D-PR-002','Rework','2026-10-18','2026-10-18')",
				"(1,32,3,1,'D-PS-001','Other project','2026-10-18','2026-10-18')",
				"(2,30,2,1,'D-PT-001','Other tenant','2026-10-18','2026-10-18')",
				"(2,34,2,2,'D-PT-002','Tenant-only delivery','2026-10-18','2026-10-18')",
			} {
				require.NoError(t, exec(insertDelivery+values))
			}
			require.NoError(t, exec("UPDATE @delivery_deliveries SET rework_of_delivery_id=30 WHERE tenant_id=1 AND id=31"))
			require.Error(t, exec(insertDelivery+"(1,33,2,1,'D-PR-001','Duplicate','2026-10-18','2026-10-18')"))
			for _, assignment := range []string{
				"project_id=999", "sequence_no=0", "version=0", "title=' '", "number=' '", "state='INVALID'", "bundle_state='INVALID'", "send_attempts=-1", "bundle_size_bytes=-1", "bundle_size_bytes=536870913", "rework_of_delivery_id=32", "rework_of_delivery_id=34", "rework_of_delivery_id=999", "rework_of_delivery_id=id",
			} {
				t.Run("delivery/"+assignment, func(t *testing.T) {
					require.Error(t, exec("UPDATE @delivery_deliveries SET "+assignment+" WHERE tenant_id=1 AND id=30"))
				})
			}
			for _, column := range []string{"release_notes_sha256", "bundle_fingerprint", "bundle_sha256", "manifest_sha256"} {
				require.Error(t, exec("UPDATE @delivery_deliveries SET "+column+"='short' WHERE tenant_id=1 AND id=30"))
				require.NoError(t, exec("UPDATE @delivery_deliveries SET "+column+"='"+strings.Repeat("a", 64)+"' WHERE tenant_id=1 AND id=30"))
			}
			for column, value := range map[string]string{"state": "DRAFT", "bundle_state": "NONE", "send_channel_kind": "EMAIL", "release_notes_actor_kind": "HUMAN"} {
				for _, invalid := range []string{"INVALID", strings.ToLower(value)} {
					t.Run("delivery_enum/"+column+"/"+invalid, func(t *testing.T) {
						require.Error(t, exec("UPDATE @delivery_deliveries SET "+column+"='"+invalid+"' WHERE tenant_id=1 AND id=30"))
					})
				}
			}
			for column, limit := range map[string]int{"send_contact_ids": 20, "source_intake_ids": 50} {
				for _, value := range []string{"invalid", "{}", "null", "[" + strings.Repeat("1,", limit) + "1]"} {
					require.Error(t, exec("UPDATE @delivery_deliveries SET "+column+"='"+value+"' WHERE tenant_id=1 AND id=30"), column)
				}
			}
			for _, state := range []string{"READY", "SENT", "ACCEPTED", "REJECTED", "PARTIALLY_ACCEPTED"} {
				require.NoError(t, exec(fmt.Sprintf("UPDATE @delivery_deliveries SET state='%s' WHERE tenant_id=1 AND id=30", state)))
				require.Error(t, exec("UPDATE @delivery_deliveries SET archived_at='2026-10-18' WHERE tenant_id=1 AND id=30"))
			}
			require.NoError(t, exec("UPDATE @delivery_deliveries SET bundle_size_bytes=536870912,state='DRAFT',archived_at='2026-10-18' WHERE tenant_id=1 AND id=30"))
			require.NoError(t, exec("UPDATE @delivery_work_items SET state='CANCELLED',archived_at='2026-10-18' WHERE tenant_id=1 AND id=20"))
			require.Error(t, exec("DELETE FROM @delivery_work_items WHERE tenant_id=1 AND id=20"))
			require.Error(t, exec("DELETE FROM @delivery_deliveries WHERE tenant_id=1 AND id=30"))
			require.Error(t, exec("DELETE FROM @delivery_requirements WHERE tenant_id=1 AND id=10"))
			require.Error(t, exec("DELETE FROM @delivery_projects WHERE tenant_id=1 AND project_id=2"))
			// Downgrade populated self-referencing tables, preserving preexisting data.
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 2))
			require.Equal(t, 3, tableRowCount(t, db, storagePrefix+"delivery_projects"))
			require.Equal(t, 4, tableRowCount(t, db, storagePrefix+"delivery_requirements"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.Zero(t, tableRowCount(t, db, storagePrefix+"delivery_work_items"))
			require.Zero(t, tableRowCount(t, db, storagePrefix+"delivery_deliveries"))
		})
	}
}

func TestDeliveryProjectModelsAndConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			for _, persisted := range []tableModel{model.CommentDefect{}, model.WorkItemActivity{}, model.CodeLink{}, model.CodeLinkScan{}, model.ArtifactConsumption{}, model.Project{}, model.ProjectContact{}, model.ProjectRepository{}, model.ProjectCounter{}, model.Requirement{}, model.AcceptanceCriterion{}, model.RequirementVersion{}, model.WorkItem{}, model.WorkItemCriterion{}, model.WorkItemContract{}, model.ReadinessCheck{}, model.WorkItemDependency{}, model.WorkItemTransition{}, model.ProjectConstitution{}, model.Assignment{}, model.ProjectStageTransition{}, model.Delivery{}, model.ResultVersion{}, model.ResultEvidence{}, model.ResultAction{}, model.ResultHandoff{}, model.VerificationRun{}, model.VerificationOverride{}} {
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
			for _, assignment := range []string{"target_date='not-a-date'", "target_date='2026-02-30'", "stage='UNKNOWN'", "stage='active'", "team_mode='hybrid'", "stage='BLOCKED'", "team_mode='AUTONOMOUS'", "budget_cap_micros=0", "budget_spent_micros=-1", "ensemble_max_branches=6", "budget_notify_pct=49"} {
				_, err := db.ExecContext(t.Context(), "UPDATE "+projects+" SET "+assignment+" WHERE tenant_id=1 AND project_id=2")
				require.Error(t, err, assignment)
			}
			// Migration 32 makes archive independent of project stage; preserve INTAKE.
			_, err := db.ExecContext(t.Context(), "UPDATE "+projects+" SET archived_at='2026-10-16 00:00:00' WHERE tenant_id=1 AND project_id=2")
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT stage FROM "+projects+" WHERE tenant_id=1 AND project_id=2 AND archived_at IS NOT NULL").Scan(&stage))
			require.Equal(t, "INTAKE", stage)
			_, err = db.ExecContext(t.Context(), "UPDATE "+projects+" SET archived_at=NULL WHERE tenant_id=1 AND project_id=2")
			require.NoError(t, err)
			contacts := deploymentPrefix + "delivery_project_contacts"
			contactInsert := "INSERT INTO " + contacts + " (tenant_id,project_id,intake_contact_id,role,can_accept,created_at,updated_at) VALUES "
			_, err = db.ExecContext(t.Context(), contactInsert+"(1,2,10,'PRIMARY',true,'2026-10-16 00:00:00','2026-10-16 00:00:00')")
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

func TestDeliveryHumanResultsConstraintsAndRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "result_pin_"
			exec := func(statement string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 8))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-20','2026-10-20','2026-10-20')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2026-10-20','2026-10-20','2026-10-20')"))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 8))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			result := "INSERT INTO @delivery_result_versions (tenant_id,id,project_id,work_item_id,sequence,summary,actor_id,baseline_version_no,created_at) VALUES "
			require.NoError(t, exec(result+"(1,30,2,20,1,'Manual output',100,0,'2026-10-20')"))
			for _, values := range []string{"(1,31,2,20,1,'Duplicate',100,0,'2026-10-20')", "(2,31,2,20,2,'Wrong tenant',100,0,'2026-10-20')", "(1,31,3,20,2,'Wrong project',100,0,'2026-10-20')", "(1,31,2,20,0,'Invalid version',100,0,'2026-10-20')", "(1,31,2,20,2,'   ',100,0,'2026-10-20')"} {
				require.Error(t, exec(result+values), values)
			}
			hash := strings.Repeat("a", 64)
			require.NoError(t, exec("INSERT INTO @delivery_result_evidence (tenant_id,id,project_id,work_item_id,result_id,binding_id,space_id,file_id,version_id,coverage,note,name,sha256) VALUES (1,40,2,20,30,50,51,52,53,'COVERED','','output.txt','"+hash+"')"))
			for _, change := range []string{"result_id=999", "project_id=999", "tenant_id=2", "coverage='UNKNOWN'", "coverage='NOT_APPLICABLE'", "sha256='bad'"} {
				require.Error(t, exec("UPDATE @delivery_result_evidence SET "+change+" WHERE id=40"), change)
			}
			require.NoError(t, exec("INSERT INTO @delivery_result_actions (tenant_id,id,project_id,work_item_id,result_id,kind,verdict,actor_id,authority_source,reason,created_at) VALUES (1,80,2,20,30,'CHECK','PASSED',101,'ROLE:delivery_lead','Checked actual evidence','2026-10-20')"))
			for _, change := range []string{"result_id=999", "kind='UNKNOWN'", "verdict='UNKNOWN'", "reason=' '", "authority_source=''"} {
				require.Error(t, exec("UPDATE @delivery_result_actions SET "+change+" WHERE id=80"), change)
			}
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 8))
				require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_result_versions"))
				require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_result_actions"))
			}
		})
	}
}

func TestDeliveryVerificationConstraintsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "verification_"
			exec := func(sql string) error {
				_, err := db.ExecContext(t.Context(), strings.ReplaceAll(sql, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 10))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-20','2026-10-20','2026-10-20')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2026-10-20','2026-10-20','2026-10-20')"))
			require.NoError(t, exec("INSERT INTO @delivery_result_versions (tenant_id,id,project_id,work_item_id,sequence,summary,actor_id,baseline_version_no,created_at) VALUES (1,30,2,20,1,'Output',100,0,'2026-10-20')"))
			require.NoError(t, exec("UPDATE @delivery_result_versions SET source_execution_id=80 WHERE id=30"))
			require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 10), "exact source snapshot cannot be destroyed before a verification run exists")
			require.NoError(t, exec("UPDATE @delivery_result_versions SET source_execution_id=NULL WHERE id=30"))
			insert := "INSERT INTO @delivery_verification_runs (tenant_id,id,project_id,work_item_id,result_id,criterion_id,method,stage,round,state,verdict,reason,actor_id,task_id,receipt_hash,created_at,completed_at) VALUES "
			require.NoError(t, exec(insert+"(1,40,2,20,30,35,'COMMAND','AUTOMATED',1,'DONE','FAILED','EXIT_NONZERO',101,50,'','2026-10-20','2026-10-20')"))
			for _, change := range []string{"result_id=999", "project_id=999", "tenant_id=2", "method='UNKNOWN'", "stage='UNKNOWN'", "state='PENDING'", "verdict='UNKNOWN'", "round=0", "method='HUMAN'"} {
				require.Error(t, exec("UPDATE @delivery_verification_runs SET "+change+" WHERE id=40"), change)
			}
			require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 10), "immutable verification history prevents destructive rollback")
			require.Equal(t, 1, tableRowCount(t, db, source.Prefix+"delivery_verification_runs"))
		})
	}
}

func TestDeliveryReadonlyReviewPlanConstraintsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "review_plan_"
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			table := source.Prefix + "delivery_plans"
			assertModelMatchesNamedTable(t, db, engine.dialect, model.ReviewPlan{}, table)
			orm := openMilestoneGORM(t, db, engine.dialect)
			future := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			row := model.ReviewPlan{ID: 2000, TenantID: 1, ProjectID: 2, WorkItemID: 20, ProposedBy: 100, SourceKind: "HUMAN", Content: "{}", ContentHash: strings.Repeat("a", 64), CoverageOK: true, Round: 1, State: "PROPOSED", CreatedAt: future, Version: 1}
			require.NoError(t, orm.Table(table).Create(&row).Error)
			var stored model.ReviewPlan
			require.NoError(t, orm.Table(table).Where("id=?", row.ID).Take(&stored).Error)
			require.True(t, future.Equal(stored.CreatedAt))
			require.Nil(t, stored.DecidedAt)
			duplicate := row
			duplicate.ID = 2001
			require.Error(t, orm.Table(table).Create(&duplicate).Error, "one immutable proposal per work round")
			duplicate.TenantID = 2
			require.NoError(t, orm.Table(table).Create(&duplicate).Error, "round is tenant scoped")
			for _, change := range []map[string]any{{"round": 0}, {"round": 4}, {"source_kind": "FABRICATED_RUNNER"}, {"state": "INVENTED"}, {"version": 0}} {
				require.Error(t, orm.Table(table).Where("id=?", row.ID).Updates(change).Error)
			}
			require.NoError(t, orm.Table(table).Where("id=?", row.ID).Updates(map[string]any{"state": "REJECTED", "feedback": "Add edge cases", "decided_by": 101, "decided_at": future, "version": 2}).Error)
			require.NoError(t, orm.Table(table).Where("id=?", row.ID).Take(&stored).Error)
			require.Equal(t, row.ContentHash, stored.ContentHash)
			require.NotNil(t, stored.DecidedAt)
			require.True(t, future.Equal(*stored.DecidedAt))
		})
	}
}

func TestDeliveryRequirementReviewSheetsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "review_sheet_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			table := source.Prefix + model.ReviewSheet{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReviewSheet{}, table)
			opinionTable := source.Prefix + model.ReviewOpinion{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReviewOpinion{}, opinionTable)
			typeTable := source.Prefix + model.ReviewType{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReviewType{}, typeTable)
			if engine.name == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			requirement := guid.ID(50)
			typeRow := model.ReviewType{ID: 300, TenantID: 1, Version: 1, Document: `{"name":"Review","propagate":true}`, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(typeTable).Create(&typeRow).Error)
			var storedType model.ReviewType
			require.NoError(t, db.Table(typeTable).Where("id = ?", typeRow.ID).First(&storedType).Error)
			require.Equal(t, typeRow.Document, storedType.Document)
			require.True(t, now.Equal(storedType.CreatedAt))
			invalidType := typeRow
			invalidType.ID = 301
			invalidType.Version = 0
			require.Error(t, db.Table(typeTable).Create(&invalidType).Error)
			row := model.ReviewSheet{ID: 100, TenantID: 1, ProjectID: 2, RequirementID: requirement, OpenRequirementID: &requirement, InitiatorID: 11, Document: `{"content":"` + strings.Repeat("a", 100000) + `"}`, State: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.ReviewSheet
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).First(&stored).Error)
			require.Equal(t, row.Document, stored.Document)
			require.True(t, now.Equal(stored.CreatedAt), "timestamp must preserve the same instant across session timezones")
			require.Equal(t, requirement, *stored.OpenRequirementID)
			firstOpinion := model.ReviewOpinion{ID: 200, TenantID: 1, ProjectID: 2, SheetID: row.ID, ReviewerID: 11, Document: `{"decision":"PASS","comment":"Original"}`, CreatedAt: now}
			require.NoError(t, db.Table(opinionTable).Create(&firstOpinion).Error)
			replacement := firstOpinion
			replacement.ID = 201
			replacement.SupersedesID = &firstOpinion.ID
			replacement.Document = `{"decision":"REJECT","comment":"Updated"}`
			require.NoError(t, db.Table(opinionTable).Create(&replacement).Error)
			var frozenOpinion model.ReviewOpinion
			require.NoError(t, db.Table(opinionTable).Where("id = ?", firstOpinion.ID).First(&frozenOpinion).Error)
			require.Equal(t, firstOpinion.Document, frozenOpinion.Document)
			require.True(t, now.Equal(frozenOpinion.CreatedAt))
			var newOpinion model.ReviewOpinion
			require.NoError(t, db.Table(opinionTable).Where("id = ?", replacement.ID).First(&newOpinion).Error)
			require.Equal(t, &firstOpinion.ID, newOpinion.SupersedesID)
			selfLinked := replacement
			selfLinked.ID = 202
			selfLinked.SupersedesID = &selfLinked.ID
			require.Error(t, db.Table(opinionTable).Create(&selfLinked).Error)
			duplicate := row
			duplicate.ID = 101
			require.Error(t, db.Table(table).Create(&duplicate).Error, "only one open review per tenant/requirement")
			bad := row
			bad.ID = 102
			bad.OpenRequirementID = nil
			bad.State = "UNKNOWN"
			require.Error(t, db.Table(table).Create(&bad).Error)
			bad.State = "PASSED"
			bad.Version = 0
			require.Error(t, db.Table(table).Create(&bad).Error)
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).Updates(map[string]any{"state": "PASSED", "open_requirement_id": nil, "version": 2}).Error)
			require.NoError(t, db.Table(table).Create(&duplicate).Error, "closed history permits a new open review")
			workID := guid.ID(70)
			workSheet := model.ReviewSheet{ID: 103, TenantID: 1, ProjectID: 2, WorkItemID: &workID, OpenWorkItemID: &workID, InitiatorID: 11, Document: `{}`, State: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(table).Create(&workSheet).Error)
			workDuplicate := workSheet
			workDuplicate.ID = 104
			require.Error(t, db.Table(table).Create(&workDuplicate).Error, "only one open review per tenant/work item")
			require.NoError(t, db.Table(table).Where("id = ?", workSheet.ID).Updates(map[string]any{"state": "PASSED", "open_work_item_id": nil, "version": 2}).Error)
			require.NoError(t, db.Table(table).Create(&workDuplicate).Error, "closed work history permits a new open review")
			require.NoError(t, db.Table(table).Where("id > ?", 0).Delete(&model.ReviewSheet{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 12))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReviewSheet{}, table)
		})
	}
}

func TestDeliveryRequirementReviewLifecycleUpgradeAndRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			raw := engine.open(t)
			raw.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "req_review_life_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 16))
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2026-10-18','2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_requirements (tenant_id,id,project_id,number,title,status,current_version_no,created_at,updated_at) VALUES (1,50,2,1,'Legacy requirement','BASELINED',1,'2026-10-18','2026-10-18')"))
			require.NoError(t, exec("INSERT INTO @delivery_requirement_versions (tenant_id,id,project_id,requirement_id,version_no,title,description,priority,criteria,content_hash,created_at) VALUES (1,60,2,50,1,'Legacy requirement','',1,'[]','"+strings.Repeat("a", 64)+"','2026-10-18')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 17))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Requirement{}, source.Prefix+model.Requirement{}.TableName())
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.RequirementVersion{}, source.Prefix+model.RequirementVersion{}.TableName())
			if engine.name == "mysql" {
				return // Schema harness; real MySQL below validates constraints and rollback.
			}
			var state, suite, hash string
			require.NoError(t, raw.QueryRowContext(t.Context(), "SELECT review_state FROM "+source.Prefix+"delivery_requirements WHERE id=50").Scan(&state))
			require.Equal(t, "NEEDS_WORK", state)
			require.NoError(t, raw.QueryRowContext(t.Context(), "SELECT acceptance_suite,suite_hash FROM "+source.Prefix+"delivery_requirement_versions WHERE id=60").Scan(&suite, &hash))
			require.Equal(t, "{}", suite)
			require.Empty(t, hash, "legacy rows must not invent a frozen suite")
			require.Error(t, exec("UPDATE @delivery_requirements SET review_state='INVALID' WHERE id=50"))
			for _, state := range []string{"REVIEW_PENDING", "APPROVED", "NEEDS_WORK"} {
				require.NoError(t, exec("UPDATE @delivery_requirements SET review_state='"+state+"' WHERE id=50"))
			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 16))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 17))
			require.NoError(t, raw.QueryRowContext(t.Context(), "SELECT review_state FROM "+source.Prefix+"delivery_requirements WHERE id=50").Scan(&state))
			require.Equal(t, "NEEDS_WORK", state)
		})
	}
}

func TestDeliveryReportQualitySchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			raw, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, raw.Close()) })
			verifyRealMySQLServer(t, raw)
			return raw
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "report_quality_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReportQualityReceipt{}, source.Prefix+model.ReportQualityReceipt{}.TableName())
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ReportQualityObservation{}, source.Prefix+model.ReportQualityObservation{}.TableName())
			if engine.name == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			var marker model.ReportQualityObservation
			require.NoError(t, db.Table(source.Prefix+marker.TableName()).First(&marker).Error)
			require.Equal(t, 1, marker.Singleton)
			require.False(t, marker.ObservedSince.IsZero())
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 22))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			at := time.Date(2040, 1, 1, 12, 0, 0, 123000000, time.UTC)
			receipt := model.ReportQualityReceipt{TenantID: 1, ID: 30, ProjectID: 2, WorkItemID: 20, Kind: "INTERNAL_QA_PASSED", SourceID: 0, Rounds: 2, OccurredAt: at}
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Create(&receipt).Error)
			var stored model.ReportQualityReceipt
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).First(&stored).Error)
			require.True(t, at.Equal(stored.OccurredAt))
			require.Nil(t, stored.ExecutorPrincipalID, "historical identity is never inferred")
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Where("id=?", stored.ID).Update("executor_principal_id", 101).Error)
			for range 2 {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 28), "recorded authorship refuses downgrade")
			}
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Where("id=?", stored.ID).First(&stored).Error)
			require.NotNil(t, stored.ExecutorPrincipalID)
			require.EqualValues(t, 101, *stored.ExecutorPrincipalID)
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Where("id=?", stored.ID).Update("executor_principal_id", nil).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 28))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			receipt.ID = 31
			require.Error(t, db.Table(source.Prefix+receipt.TableName()).Create(&receipt).Error, "one immutable first successful gate per work item")
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 22), "source facts must survive a refused downgrade")
			require.NoError(t, db.Table(source.Prefix+receipt.TableName()).Where("tenant_id=?", 1).Delete(&model.ReportQualityReceipt{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 22), "empty source can downgrade after a prior refusal")
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))

		})
	}
}

func TestDeliveryClarificationReceiptConstraintsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "q_receipt_"
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			exec := func(statement string) error {
				_, err := db.Exec(strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'RECEIPT','2040-01-01','2040-01-01','2040-01-01')"))
			insert := "INSERT INTO @delivery_clarification_receipts(id,tenant_id,project_id,clarification_id,reply_version,handled_by,handled_at) VALUES "
			require.NoError(t, exec(insert+"(1,1,2,3,1,4,'2040-01-02')"))
			require.Error(t, exec(insert+"(2,1,2,3,1,4,'2040-01-02')"))
			require.NoError(t, exec(insert+"(2,1,2,3,2,4,'2040-01-02')"))
			for _, change := range []string{"reply_version=0", "handled_by=0", "project_id=99", "tenant_id=99"} {
				require.Error(t, exec("UPDATE @delivery_clarification_receipts SET "+change+" WHERE id=1"))
			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 24))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
		})
	}
}

func TestDeliveryProjectModeHistoryUpgradeAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			raw := engine.open(t)
			raw.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "q_mode_"
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 25))
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'MODE','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 26))
			table := source.Prefix + model.ProjectModeChange{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ProjectModeChange{}, table)
			require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+"delivery_projects"), "upgrade preserves legacy projects")
			require.Zero(t, tableRowCount(t, raw, table), "upgrade must not fabricate mode history")
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			first := model.ProjectModeChange{TenantID: 1, ID: 10, ProjectID: 2, FromMode: "HUMAN_ONLY", ToMode: "HYBRID", Trigger: "MANUAL", Reason: "Enable assistance", PreconditionsSnapshot: `{"ready":true}`, RevokedDelegationIDs: `[]`, RevokedSpawnGrantIDs: `[]`, ActorID: 100, AuthoritySource: "ROLE:delivery_lead", VersionAfter: 2, OccurredAt: at}
			require.NoError(t, db.Table(table).Create(&first).Error)
			second := first
			second.ID, second.VersionAfter = 11, 3
			second.FromMode, second.ToMode, second.Trigger = "HYBRID", "HYBRID", "TAKEOVER"
			second.Reason, second.RevokedDelegationIDs, second.RevokedSpawnGrantIDs = "Human takeover", `[21]`, `[31]`
			second.OccurredAt = at.Add(time.Hour)
			require.NoError(t, db.Table(table).Create(&second).Error, "same-mode takeover retains a distinct receipt")
			var stored model.ProjectModeChange
			require.NoError(t, db.Table(table).Where("tenant_id=1 AND id=10").First(&stored).Error)
			require.Equal(t, first.Reason, stored.Reason)
			require.JSONEq(t, first.PreconditionsSnapshot, stored.PreconditionsSnapshot)
			require.JSONEq(t, first.RevokedDelegationIDs, stored.RevokedDelegationIDs)
			require.JSONEq(t, first.RevokedSpawnGrantIDs, stored.RevokedSpawnGrantIDs)
			require.True(t, at.Equal(stored.OccurredAt), "2040 timestamp preserves its instant and milliseconds")
			stored = model.ProjectModeChange{}
			require.NoError(t, db.Table(table).Where("tenant_id=1 AND id=11").First(&stored).Error)
			require.Equal(t, second.Reason, stored.Reason)
			require.JSONEq(t, second.RevokedDelegationIDs, stored.RevokedDelegationIDs)
			require.JSONEq(t, second.RevokedSpawnGrantIDs, stored.RevokedSpawnGrantIDs)
			duplicate := first
			duplicate.ID = 12
			require.Error(t, db.Table(table).Create(&duplicate).Error, "one receipt per resulting project version")
			for _, change := range []string{"from_mode='UNKNOWN'", "to_mode='UNKNOWN'", "trigger_kind='UNKNOWN'", "version_after=1", "id=0", "id=-1", "actor_id=0", "actor_id=-1", "project_id=0", "project_id=999", "tenant_id=0", "tenant_id=999"} {
				require.Error(t, exec("UPDATE @delivery_project_mode_changes SET "+change+" WHERE id=10"), change)
			}
			require.Error(t, exec("DELETE FROM @delivery_projects WHERE tenant_id=1 AND project_id=2"), "project history restricts parent deletion")
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 25), "refused downgrade preserves history")
				require.Equal(t, 2, tableRowCount(t, raw, table))
			}
			require.NoError(t, exec("DELETE FROM @delivery_project_mode_changes WHERE tenant_id=1"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 25))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 26))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ProjectModeChange{}, table)
		})
	}
}

func TestDeliveryConfirmationUpgradeAndWithdrawalAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			raw := engine.open(t)
			raw.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "q_confirm_"
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 26))
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'CONFIRM','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 27))
			table := source.Prefix + model.DeliveryConfirmation{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.DeliveryConfirmation{}, table)
			require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+"delivery_projects"))
			require.Zero(t, tableRowCount(t, raw, table), "upgrade must not fabricate confirmation requests")
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			active := "ACTIVE"
			first := model.DeliveryConfirmation{ID: 10, TenantID: 1, ProjectID: 2, PackageID: 20, SourceVersion: 1, ActiveKey: &active, RequestedBy: 100, RequestedAt: at, Version: 1}
			require.NoError(t, db.Table(table).Create(&first).Error)
			duplicate := first
			duplicate.ID = 11
			require.Error(t, db.Table(table).Create(&duplicate).Error, "one active request per exact source revision")
			for _, change := range []string{"active_key=NULL", "active_key='OTHER'", "withdrawn_at='2040-01-03'", "withdrawn_by=101", "source_version=0", "package_id=0", "requested_by=0", "version=0", "project_id=999", "tenant_id=999"} {
				require.Error(t, exec("UPDATE @delivery_confirmation_requests SET "+change+" WHERE id=10"), change)
			}
			require.Error(t, exec("DELETE FROM @delivery_projects WHERE tenant_id=1 AND project_id=2"), "request restricts parent deletion")
			withdrawnAt, withdrawnBy := at.Add(time.Hour), guid.ID(101)
			require.NoError(t, db.Table(table).Where("id=10").Updates(map[string]any{"active_key": nil, "withdrawn_at": withdrawnAt, "withdrawn_by": withdrawnBy, "version": 2}).Error)
			for _, change := range []string{"active_key='ACTIVE'", "withdrawn_at=NULL", "withdrawn_by=NULL", "withdrawn_by=0"} {
				require.Error(t, exec("UPDATE @delivery_confirmation_requests SET "+change+" WHERE id=10"), change)
			}
			require.NoError(t, db.Table(table).Create(&duplicate).Error, "withdrawn history permits requesting the same revision again")
			secondHistory := first
			secondHistory.ID, secondHistory.ActiveKey, secondHistory.WithdrawnAt, secondHistory.WithdrawnBy, secondHistory.Version = 12, nil, &withdrawnAt, &withdrawnBy, 2
			require.NoError(t, db.Table(table).Create(&secondHistory).Error, "multiple terminal histories use SQL NULL uniqueness semantics")
			nextRevision := first
			nextRevision.ID, nextRevision.SourceVersion = 13, 2
			require.NoError(t, db.Table(table).Create(&nextRevision).Error, "uniqueness includes source revision")
			var stored model.DeliveryConfirmation
			require.NoError(t, db.Table(table).Where("id=10").First(&stored).Error)
			require.True(t, at.Equal(stored.RequestedAt))
			require.NotNil(t, stored.WithdrawnAt)
			require.True(t, withdrawnAt.Equal(*stored.WithdrawnAt))
			require.Equal(t, &withdrawnBy, stored.WithdrawnBy)
			require.Nil(t, stored.ActiveKey)
			require.Equal(t, 4, tableRowCount(t, raw, table))
			for retry := 0; retry < 2; retry++ {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 26), "refused downgrade preserves active and withdrawn requests")
				require.Equal(t, 4, tableRowCount(t, raw, table))
				var history model.DeliveryConfirmation
				require.NoError(t, db.Table(table).Where("id=10").First(&history).Error)
				require.True(t, at.Equal(history.RequestedAt))
				require.NotNil(t, history.WithdrawnAt)
				require.True(t, withdrawnAt.Equal(*history.WithdrawnAt))
				require.Equal(t, &withdrawnBy, history.WithdrawnBy)
				require.Nil(t, history.ActiveKey)
			}
			require.NoError(t, exec("DELETE FROM @delivery_confirmation_requests WHERE tenant_id=1"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 26))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 27))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.DeliveryConfirmation{}, table)
		})
	}
}

func TestDeliveryAutonomyVersionUpgradeAndRefusedRollbackAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
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
			raw := engine.open(t)
			raw.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "q_autonomy_"
			exec := func(statement string) error {
				_, err := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return err
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 27))
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'AUTONOMY','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 28))
			table := source.Prefix + model.Project{}.TableName()
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Project{}, table)
			read := func() sql.NullInt64 {
				var version sql.NullInt64
				require.NoError(t, raw.QueryRowContext(t.Context(), "SELECT autonomy_version FROM "+table+" WHERE tenant_id=1 AND project_id=2").Scan(&version))
				return version
			}
			require.False(t, read().Valid, "upgrade must not fabricate a legacy episode")
			for _, version := range []int{0, -1} {
				require.Error(t, exec(fmt.Sprintf("UPDATE @delivery_projects SET autonomy_version=%d WHERE tenant_id=1 AND project_id=2", version)), "only positive episode revisions persist")
			}
			require.NoError(t, exec("UPDATE @delivery_projects SET autonomy_version=4,version=4 WHERE tenant_id=1 AND project_id=2"))
			for range 2 {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 27), "nonempty episode state refuses repeated downgrade")
				version := read()
				require.True(t, version.Valid)
				require.Equal(t, int64(4), version.Int64)
			}
			require.NoError(t, exec("UPDATE @delivery_projects SET autonomy_version=NULL WHERE tenant_id=1 AND project_id=2"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 27), "explicitly empty episode state permits downgrade after refusal")
			require.Equal(t, 1, tableRowCount(t, raw, table))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 28))
			require.False(t, read().Valid)
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Project{}, table)
		})
	}
}

func TestDeliveryAttentionSchemasAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "attention_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.AttentionSession{}, source.Prefix+(model.AttentionSession{}).TableName())
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ResultAction{}, source.Prefix+(model.ResultAction{}).TableName())
			exec := func(statement string) error {
				_, e := raw.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return e
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Now().UTC().Truncate(time.Millisecond)
			claim := model.ResultVersion{TenantID: 1, ID: 21, ProjectID: 2, WorkItemID: 20, Sequence: 1, Summary: "Claim", ActorID: 100, VerificationPlan: "{}", CreatedAt: at}
			require.NoError(t, db.Table(source.Prefix+claim.TableName()).Create(&claim).Error)
			session := model.AttentionSession{TenantID: 1, ID: 22, ProjectID: 2, WorkItemID: 20, ClaimID: &claim.ID, PrincipalID: 101, Surface: "REVIEW_DESK", StartedAt: at, LastHeartbeatAt: at}
			table := source.Prefix + session.TableName()
			require.NoError(t, db.Table(table).Omit("InteractionID").Create(&session).Error)
			for range 2 {
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 29))
				require.Equal(t, 1, tableRowCount(t, raw, table))
			}
			require.NoError(t, db.Table(table).Where("id=22").Delete(&model.AttentionSession{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 29))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestDeliveryProjectBudgetEventsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "budget_event_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ProjectBudgetEvent{}, source.Prefix+"delivery_project_budget_events")
			exec := func(sql string) error {
				_, e := raw.ExecContext(t.Context(), strings.ReplaceAll(sql, "@", source.Prefix))
				return e
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'BU','2040-01-01','2040-01-01','2040-01-01')"))
			insert := "INSERT INTO @delivery_project_budget_events(tenant_id,id,project_id,kind,threshold_lock,threshold_pct,authority_source,cap_micros,spent_micros,notify_pct,cap_version,state,reason,occurred_at) VALUES "
			require.NoError(t, exec(insert+"(1,10,2,'THRESHOLD_NOTIFY','THRESHOLD_NOTIFY',80,'budget:threshold',100,80,80,1,'NOTIFIED','','2040-01-01')"))
			require.Error(t, exec(insert+"(1,11,2,'THRESHOLD_NOTIFY','THRESHOLD_NOTIFY',80,'budget:threshold',100,80,80,1,'NOTIFIED','','2040-01-01')"))
			for _, change := range []string{"kind='BOGUS'", "state='BOGUS'", "threshold_lock=NULL", "threshold_pct=100", "spent_micros=-1", "project_id=99", "cap_version=0"} {
				require.Error(t, exec("UPDATE @delivery_project_budget_events SET "+change+" WHERE id=10"), change)
			}
			require.NoError(t, exec(insert+"(1,12,2,'CAP_CHANGED',NULL,NULL,'permission:project_manage',100,80,80,2,'NORMAL','Explained adjustment','2040-01-01')"))
			require.NoError(t, exec(insert+"(1,13,2,'HOLD_RELEASED',NULL,NULL,'permission:project_manage',200,80,80,2,'NORMAL','Explained adjustment','2040-01-01')"))
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 30))
			require.Equal(t, 3, tableRowCount(t, raw, source.Prefix+"delivery_project_budget_events"))
		})
	}
}

// Archive state is independent of business stage. Referenced identities survive.
func TestDeliveryIndependentArchiveUpgradeAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			db.SetMaxOpenConns(1)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "archive_upgrade_"
			exec := func(statement string) error {
				_, e := db.ExecContext(t.Context(), strings.ReplaceAll(statement, "@", source.Prefix))
				return e
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 31))
			require.NoError(t, exec("INSERT INTO @delivery_projects (tenant_id,project_id,code,stage,constitution_version_no,autonomy_version,stage_changed_at,created_at,updated_at) VALUES (1,2,'PR','ACTIVE',3,4,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items (tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,20,2,1,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 32))
			check := func() {
				var stage string
				var constitution, autonomy, project int
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT stage,constitution_version_no,autonomy_version FROM "+source.Prefix+"delivery_projects WHERE project_id=2").Scan(&stage, &constitution, &autonomy))
				require.Equal(t, "ACTIVE", stage)
				require.Equal(t, 3, constitution)
				require.Equal(t, 4, autonomy)
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT project_id FROM "+source.Prefix+"delivery_work_items WHERE id=20").Scan(&project))
				require.Equal(t, 2, project)
				require.Error(t, exec("DELETE FROM @delivery_projects WHERE project_id=2"))
				require.Error(t, exec("UPDATE @delivery_projects SET code='invalid code' WHERE project_id=2"))
			}
			check()
			require.NoError(t, exec("UPDATE @delivery_projects SET archived_at='2040-02-01' WHERE project_id=2"))
			// The MySQL schema harness cannot enforce added CHECK over existing data.
			if engine.name != "mysql" {
				require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 31))
				check()
			}
			require.NoError(t, exec("UPDATE @delivery_projects SET archived_at=NULL WHERE project_id=2"))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 31))
			check()
			require.Error(t, exec("UPDATE @delivery_projects SET archived_at='2040-02-01' WHERE project_id=2"))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 32))
			check()
		})
	}
}

func TestDeliveryCommentFeedbackMigrationAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "cfeedback_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 36))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.ResultFeedback{}).TableName()
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 37))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.ResultFeedback{}, table)
				require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 36))
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 37))
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			require.NoError(t, db.Exec("INSERT INTO "+source.Prefix+"delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(7,3,'CF','2040-01-01','2040-01-01','2040-01-01')").Error)
			require.NoError(t, db.Exec("INSERT INTO "+source.Prefix+"delivery_work_items(tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES(7,4,3,1,'Work','TASK',0,8,'2040-01-01','2040-01-01','2040-01-01')").Error)
			result := model.ResultVersion{TenantID: 7, ID: 5, ProjectID: 3, WorkItemID: 4, Sequence: 1, Summary: "Pinned result", ActorID: 8, CreatedAt: at}
			require.NoError(t, db.Table(source.Prefix+result.TableName()).Create(&result).Error)
			// Legacy comments may repeat NULL source pointers; converted sources may not.
			legacy := model.ResultFeedback{TenantID: 7, ID: 1, ProjectID: 3, WorkItemID: 4, ResultVersionID: 5, Kind: "COMMENT", Detail: "legacy", ActorID: 8, CreatedAt: at}
			require.NoError(t, db.Table(table).Create(&legacy).Error)
			legacy.ID = 2
			require.NoError(t, db.Table(table).Create(&legacy).Error)
			thread := guid.ID(20)
			anchor := `{"kind":"CODE_LINES","version_ref":"file:70@71"}`
			converted := legacy
			converted.ID = 3
			converted.Kind = "FEEDBACK"
			converted.Category = "QUALITY_REJECTION"
			converted.CommentThreadID = &thread
			converted.AnchorSnapshot = &anchor
			converted.Location = "src/a.go:2-3"
			converted.Expected = "Correct behavior"
			require.NoError(t, db.Table(table).Create(&converted).Error)
			converted.ID = 4
			require.Error(t, db.Table(table).Create(&converted).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 36))
			var retained model.ResultFeedback
			require.NoError(t, db.Table(table).Where("id = ?", 3).Take(&retained).Error)
			require.Equal(t, &anchor, retained.AnchorSnapshot)
			require.Equal(t, &thread, retained.CommentThreadID)
		})
	}
}

func TestDeliveryAgentReviewPlanPopulatedUpgradeAcrossEngines(t *testing.T) {
	engines := append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "agent_plan_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 34))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.ReviewPlan{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			legacy := model.ReviewPlan{ID: 1, TenantID: 7, ProjectID: 3, WorkItemID: 4, ProposedBy: 8, SourceKind: "HUMAN", Content: `{"legacy":"immutable"}`, ContentHash: strings.Repeat("a", 64), CoverageOK: true, Round: 1, State: "PROPOSED", CreatedAt: at, Version: 1}
			require.NoError(t, db.Table(table).Create(&legacy).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 35))
			agent := legacy
			agent.ID, agent.Round, agent.SourceKind = 2, 2, "AGENT"
			agent.Content = `{"producer":{"execution_id":"42"}}`
			agent.ContentHash = strings.Repeat("b", 64)
			require.NoError(t, db.Table(table).Create(&agent).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 34), "Agent provenance must block downgrade before its schema version is removed")
			for _, expected := range []model.ReviewPlan{legacy, agent} {
				var retained model.ReviewPlan
				require.NoError(t, db.Table(table).Where("id = ?", expected.ID).Take(&retained).Error)
				require.Equal(t, expected.SourceKind, retained.SourceKind)
				require.Equal(t, expected.Content, retained.Content)
				require.Equal(t, expected.ContentHash, retained.ContentHash)
				require.True(t, at.Equal(retained.CreatedAt))
			}
			require.NoError(t, db.Table(table).Where("id = ?", agent.ID).Delete(&model.ReviewPlan{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 34))
			var downgraded model.ReviewPlan
			require.NoError(t, db.Table(table).Where("id = ?", legacy.ID).Take(&downgraded).Error)
			require.Equal(t, legacy.Content, downgraded.Content)
			require.Equal(t, legacy.ContentHash, downgraded.ContentHash)
			require.Equal(t, "HUMAN", downgraded.SourceKind)
			require.True(t, at.Equal(downgraded.CreatedAt))
			require.Error(t, db.Table(table).Create(&agent).Error, "successful downgrade must restore the Human-only source constraint")
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 35))
			require.NoError(t, db.Table(table).Create(&agent).Error, "a refused downgrade must not poison a later downgrade/upgrade retry")
		})
	}
}
