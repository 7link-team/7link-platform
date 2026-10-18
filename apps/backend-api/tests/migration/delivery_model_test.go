package migration_test

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/delivery/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

// This tests storage invariants only. It does not bypass the still-pending
// work-item commands, decisions, delivery assembly or sending workflows.
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
			for _, persisted := range []tableModel{model.Project{}, model.ProjectContact{}, model.ProjectRepository{}, model.ProjectCounter{}, model.Requirement{}, model.AcceptanceCriterion{}, model.RequirementVersion{}, model.WorkItem{}, model.Delivery{}} {
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
