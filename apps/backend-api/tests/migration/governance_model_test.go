package migration_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/governance/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGovernanceRoleSeedsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
		{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			dsn := os.Getenv("MYSQL8_DSN")
			if dsn == "" {
				t.Skip("run through make test-mysql8")
			}
			raw, e := sql.Open("mysql", dsn)
			require.NoError(t, e)
			t.Cleanup(func() { require.NoError(t, raw.Close()) })
			verifyRealMySQLServer(t, raw)
			return raw
		}},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&governance.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			table := deploymentPrefix + "governance_role_seeds"
			orm := openMilestoneGORM(t, db, engine.dialect)
			decisions := deploymentPrefix + "governance_workflow_decisions"
			at := time.Date(2026, 10, 20, 1, 2, 3, 456000000, time.UTC)
			decision := model.WorkflowDecision{ID: 500, TenantID: 1, ProjectID: 20, BindingID: 30, DefinitionHash: strings.Repeat("a", 64), ExpansionKey: "gate/team", Gate: "TEAM", Outcome: "APPROVE", Reason: "Reviewed scope", ActorID: 2, AuthoritySource: "IAM:workflow_gate_team", CreatedAt: at}
			require.NoError(t, orm.Table(decisions).Create(&decision).Error)
			var stored model.WorkflowDecision
			require.NoError(t, orm.Table(decisions).Where("id=?", 500).Take(&stored).Error)
			require.True(t, at.Equal(stored.CreatedAt))
			decision.ID = 501
			require.Error(t, orm.Table(decisions).Create(&decision).Error)
			decision.TenantID = 2
			require.NoError(t, orm.Table(decisions).Create(&decision).Error)

			assertModelMatchesNamedTable(t, db, engine.dialect, model.RoleSeed{}, table)
			assertModelMatchesNamedTable(t, db, engine.dialect, model.Settings{}, deploymentPrefix+"governance_settings")
			projectRules := deploymentPrefix + "governance_project_escalation_rules"
			assertModelMatchesNamedTable(t, db, engine.dialect, model.ProjectEscalationRule{}, projectRules)
			projectRule := model.ProjectEscalationRule{ID: 800, TenantID: 1, ProjectID: 20, Kind: "LOW_SELF_CONFIDENCE", ValueJSON: `{"enabled":true}`, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, orm.Table(projectRules).Create(&projectRule).Error)
			projectRule.ID = 801
			require.Error(t, orm.Table(projectRules).Create(&projectRule).Error, "one override per tenant, project, kind")
			projectRule.ProjectID = 21
			require.NoError(t, orm.Table(projectRules).Create(&projectRule).Error)
			projectRule.ID = 802
			projectRule.TenantID = 2
			require.NoError(t, orm.Table(projectRules).Create(&projectRule).Error)
			assertModelMatchesNamedTable(t, db, engine.dialect, model.AutomatedDecision{}, deploymentPrefix+"governance_automated_decisions")
			assertModelMatchesNamedTable(t, db, engine.dialect, model.SpawnRequest{}, deploymentPrefix+"governance_spawn_requests")
			delegations := deploymentPrefix + "governance_delegations"
			assertModelMatchesNamedTable(t, db, engine.dialect, model.Delegation{}, delegations)
			future := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			assertSpawnGrantSchema(t, db, orm, engine.dialect, future)
			assertQualityEscapeSchema(t, db, orm, engine.dialect, future)
			receipts := deploymentPrefix + "governance_claim_evaluations"
			assertModelMatchesNamedTable(t, db, engine.dialect, model.ClaimEvaluation{}, receipts)
			receipt := model.ClaimEvaluation{ID: 700, TenantID: 1, ProjectID: 20, WorkItemID: 30, ClaimID: 40, TriggerEventID: 50, LowConfidenceResult: "PASS", SensitivePathResult: "UNAVAILABLE", EvaluatedAt: future}
			require.NoError(t, orm.Table(receipts).Create(&receipt).Error)
			var persisted model.ClaimEvaluation
			require.NoError(t, orm.Table(receipts).Where("id=?", 700).Take(&persisted).Error)
			require.True(t, future.Equal(persisted.EvaluatedAt))
			receipt.ID = 701
			require.Error(t, orm.Table(receipts).Create(&receipt).Error, "exact claim has one immutable receipt")
			receipt.TenantID = 2
			require.NoError(t, orm.Table(receipts).Create(&receipt).Error, "claim uniqueness is tenant scoped")
			receipt.ID = 702
			receipt.ClaimID = 41
			receipt.LowConfidenceResult = "INVENTED"
			require.Error(t, orm.Table(receipts).Create(&receipt).Error, "invalid evaluation result rejected")
			activeKey := "3:20"
			grant := model.Delegation{ID: 600, TenantID: 1, ProjectID: 20, GranteePrincipalID: 3, GranteeAgentID: 4, GrantorPrincipalID: 2, ConditionsJSON: `{"actions":["decision_approve"],"max_risk":"LOW","cost_cap_micros":1,"max_rework":3}`, IAMBindings: `[]`, StartsAt: future, EndsAt: future.Add(time.Hour), Reason: "Low risk only", State: "ACTIVE", ActiveKey: &activeKey, Version: 1, CreatedAt: future, UpdatedAt: future}
			require.NoError(t, orm.Table(delegations).Create(&grant).Error)
			var saved model.Delegation
			require.NoError(t, orm.Table(delegations).Where("id=?", 600).Take(&saved).Error)
			require.True(t, future.Add(time.Hour).Equal(saved.EndsAt))
			require.Nil(t, saved.EndedAt)
			grant.ID = 601
			require.Error(t, orm.Table(delegations).Create(&grant).Error, "one active delegation per tenant/grantee/project")
			grant.ActiveKey = nil
			require.Error(t, orm.Table(delegations).Create(&grant).Error, "active authority cannot lack its unique slot")
			grant.ActiveKey = &activeKey
			grant.TenantID = 2
			grant.EndsAt = future
			require.Error(t, orm.Table(delegations).Create(&grant).Error, "nonpositive window rejected")
			grant.EndsAt = future.Add(time.Hour)
			require.NoError(t, orm.Table(delegations).Create(&grant).Error, "different tenant has a separate slot")
			ended := future.Add(time.Hour)
			endReason := "MANUAL"
			grant.ID = 602
			grant.State = "REVOKED"
			grant.ActiveKey = nil
			grant.EndedAt = &ended
			grant.EndReason = &endReason
			require.NoError(t, orm.Table(delegations).Create(&grant).Error)
			grant.ID = 603
			require.NoError(t, orm.Table(delegations).Create(&grant).Error, "terminal rows release the unique slot")
			assertModelMatchesNamedTable(t, db, engine.dialect, model.WorkflowDecision{}, deploymentPrefix+"governance_workflow_decisions")
			assertModelMatchesNamedTable(t, db, engine.dialect, model.EscalationRule{}, deploymentPrefix+"governance_escalation_rules")
			assertModelMatchesNamedTable(t, db, engine.dialect, model.EscalationIncident{}, deploymentPrefix+"governance_escalation_incidents")
			insert := func(id, tenant, key, role string) error {
				_, err := db.ExecContext(t.Context(), "INSERT INTO "+table+" (id,tenant_id,role_key,role_id,manifest_version,applied_codes,skipped_codes,created_at,updated_at) VALUES ("+id+","+tenant+",'"+key+"',"+role+",1,'[]','[]','2026-10-16 00:00:00','2026-10-16 00:00:00')")
				return err
			}
			require.NoError(t, insert("1", "1", "delivery_lead", "11"))
			require.NoError(t, insert("2", "2", "delivery_lead", "11"), "keys and role references are tenant qualified")
			require.Error(t, insert("3", "1", "delivery_lead", "12"), "a role key has one owner")
			require.Error(t, insert("3", "1", "engineer", "11"), "a role cannot back two seed keys")
			require.Error(t, insert("3", "1", strings.Repeat("x", 49), "12"), "role key length is enforced")
			var managed, conflict bool
			var version int
			var applied, skipped string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT managed,seed_conflict,version,applied_codes,skipped_codes FROM "+table+" WHERE id=1").Scan(&managed, &conflict, &version, &applied, &skipped))
			require.True(t, managed)
			require.False(t, conflict)
			require.Equal(t, 1, version)
			require.JSONEq(t, "[]", applied)
			require.JSONEq(t, "[]", skipped)
			_, err := db.ExecContext(t.Context(), "UPDATE "+table+" SET applied_codes='not-json' WHERE id=1")
			require.Error(t, err)
			_, err = db.ExecContext(t.Context(), "UPDATE "+table+" SET managed=false,seed_conflict=true,applied_codes='[\"project_read\"]',skipped_codes='[\"work_item_read\"]' WHERE id=1")
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT managed,seed_conflict,applied_codes,skipped_codes FROM "+table+" WHERE id=1").Scan(&managed, &conflict, &applied, &skipped))
			require.False(t, managed)
			require.True(t, conflict)
			require.JSONEq(t, `["project_read"]`, applied)
			require.JSONEq(t, `["work_item_read"]`, skipped)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 2))
			require.False(t, orm.Migrator().HasTable(decisions))
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			assertModelMatchesNamedTable(t, db, engine.dialect, model.WorkflowDecision{}, decisions)

		})
	}
}

func assertSpawnGrantSchema(t *testing.T, db *sql.DB, orm *gorm.DB, dialect string, future time.Time) {
	t.Helper()
	grants, members, cursors := deploymentPrefix+"governance_spawn_grants", deploymentPrefix+"governance_spawn_grant_members", deploymentPrefix+"governance_spawn_cost_cursors"
	assertModelMatchesNamedTable(t, db, dialect, model.SpawnGrant{}, grants)
	assertModelMatchesNamedTable(t, db, dialect, model.SpawnGrantMember{}, members)
	assertModelMatchesNamedTable(t, db, dialect, model.SpawnCostCursor{}, cursors)
	key := "33"
	grant := model.SpawnGrant{ID: 800, TenantID: 1, GranteeAgentID: 33, GranteePrincipalID: 3, GrantedBy: 2, ScopeJSON: `{}`, Reason: "Approved bounded capacity", StartsAt: future, EndsAt: future.Add(time.Hour), State: "ACTIVE", ActiveKey: &key, SpawnedDay: "2040-01-02", Version: 1, CreatedAt: future, UpdatedAt: future}
	require.NoError(t, orm.Table(grants).Create(&grant).Error)
	var saved model.SpawnGrant
	require.NoError(t, orm.Table(grants).Where("id=?", grant.ID).Take(&saved).Error)
	require.True(t, grant.EndsAt.Equal(saved.EndsAt), "year 2040 survives all dialects")
	grant.ID = 801
	require.Error(t, orm.Table(grants).Create(&grant).Error, "one live supervisor slot")
	grant.TenantID = 2
	require.NoError(t, orm.Table(grants).Create(&grant).Error, "tenant slots are independent")
	grant.ID, grant.ActiveCount = 802, -1
	require.Error(t, orm.Table(grants).Create(&grant).Error, "negative usage rejected")
	grant.ActiveCount, grant.ActiveKey = 0, nil
	require.Error(t, orm.Table(grants).Create(&grant).Error, "live grant needs a unique slot")
	member := model.SpawnGrantMember{ID: 900, TenantID: 1, GrantID: 800, AgentID: 44, State: "ACTIVE", PerAgentCapMicros: 1, ReservedAt: future}
	require.NoError(t, orm.Table(members).Create(&member).Error)
	member.ID = 901
	require.Error(t, orm.Table(members).Create(&member).Error, "child cannot reserve twice")
	member.AgentID, member.State = 45, "RELEASED"
	require.Error(t, orm.Table(members).Create(&member).Error, "release needs terminal facts")
	cursor := model.SpawnCostCursor{ID: 950, TenantID: 1, ExecutionID: 55, AgentID: 44, UpdatedAt: future}
	require.NoError(t, orm.Table(cursors).Create(&cursor).Error)
	cursor.ID = 951
	require.Error(t, orm.Table(cursors).Create(&cursor).Error, "execution cursor unique within tenant")
	cursor.ExecutionID, cursor.LastCumulativeMicros = 56, -1
	require.Error(t, orm.Table(cursors).Create(&cursor).Error, "negative cumulative cost rejected")
}

func assertQualityEscapeSchema(t *testing.T, db *sql.DB, orm *gorm.DB, dialect string, future time.Time) {
	t.Helper()
	table := deploymentPrefix + "governance_quality_escapes"
	assertModelMatchesNamedTable(t, db, dialect, model.QualityEscape{}, table)
	row := model.QualityEscape{ID: 1100, TenantID: 1, ProjectID: 20, WorkItemID: 30, SourceEventID: 1200, EscapeKey: "30:40", Stage: "HUMAN_REVIEW", Category: "FUNCTIONAL_DEFECT", Summary: "Actual missed requirement", PassedBy: "[]", LearningState: "PENDING", MemoryIDs: "[]", CreatedAt: future, Version: 1}
	require.NoError(t, orm.Table(table).Create(&row).Error)
	var stored model.QualityEscape
	require.NoError(t, orm.Table(table).Where("id=?", row.ID).Take(&stored).Error)
	require.True(t, future.Equal(stored.CreatedAt))
	require.Nil(t, stored.DecidedAt)
	duplicate := row
	duplicate.ID = 1101
	duplicate.EscapeKey = "30:41"
	require.Error(t, orm.Table(table).Create(&duplicate).Error, "one projection per immutable source event")
	duplicate.SourceEventID = 1201
	duplicate.EscapeKey = row.EscapeKey
	require.Error(t, orm.Table(table).Create(&duplicate).Error, "one escape key per tenant")
	duplicate.TenantID = 2
	require.NoError(t, orm.Table(table).Create(&duplicate).Error, "escape key is tenant scoped")
	for _, change := range []map[string]any{{"stage": "AUTOMATED"}, {"learning_state": "INVENTED"}, {"learning_state": "RECORDED"}, {"version": 0}} {
		require.Error(t, orm.Table(table).Where("id=?", row.ID).Updates(change).Error)
	}
	require.NoError(t, orm.Table(table).Where("id=?", row.ID).Updates(map[string]any{"learning_state": "RECORDED", "decided_by": 2, "decided_at": future}).Error)
	require.NoError(t, orm.Table(table).Where("id=?", row.ID).Take(&stored).Error)
	require.NotNil(t, stored.DecidedAt)
	require.True(t, future.Equal(*stored.DecidedAt))
}
