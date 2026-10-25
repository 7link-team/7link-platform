package migration_test

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAgentsSchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&agents.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ProcessEvent{}, deploymentPrefix+"agents_process_events")
			for _, item := range []struct {
				row   tableModel
				table string
			}{{model.LiveState{}, "agents_live_states"}, {model.InboxEvent{}, "agents_inbox_events"}, {model.Reset{}, "agents_resets"}, {model.Reminder{}, "agents_reminders"}, {model.ToolEntry{}, "agents_tool_entries"}, {model.ToolGrant{}, "agents_tool_grants"}, {model.ToolVersion{}, "agents_tool_versions"}, {model.ToolUpstream{}, "agents_tool_upstreams"}, {model.ManifestVersion{}, "agents_operation_manifest_versions"}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, item.row, deploymentPrefix+item.table)
			}
			if engine.name == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			tool := model.ToolEntry{ID: 991, TenantID: 1, EntryKey: "credential-metadata", Kind: "MCP_SERVER", Title: "Remote", Transport: "STREAMABLE_HTTP", State: "ACTIVE", CredentialLast4: "1234", CredentialRotatedAt: &now, UpstreamAuthFailures: 5, UpstreamAuthNotifiedAt: &now, Version: 1, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"agents_tool_entries").Create(&tool).Error)
			var stored model.ToolEntry
			require.NoError(t, db.Table(deploymentPrefix+"agents_tool_entries").Where("id = ?", tool.ID).First(&stored).Error)
			require.Equal(t, "1234", stored.CredentialLast4)
			require.Equal(t, 5, stored.UpstreamAuthFailures)
			require.NotNil(t, stored.CredentialRotatedAt)
			require.NotNil(t, stored.UpstreamAuthNotifiedAt)
			require.WithinDuration(t, now, *stored.CredentialRotatedAt, time.Millisecond)
			require.WithinDuration(t, now, *stored.UpstreamAuthNotifiedAt, time.Millisecond)
			for i, ack := range []int64{0, 2, -1} {
				r := model.LiveState{AgentPrincipalID: guid.ID(i + 1), TenantID: 1, Head: 1, Ack: ack, Activity: "OFFLINE", SessionEpoch: 1, WorkspaceEpoch: 1, UpdatedAt: now}
				err := db.Table(deploymentPrefix + "agents_live_states").Create(&r).Error
				if i == 0 {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
			row := model.InboxEvent{ID: 20, TenantID: 1, AgentPrincipalID: 1, Seq: 1, SourceEventID: "source:1", Kind: "STATE", SubjectID: 1, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"agents_inbox_events").Create(&row).Error)
			row.ID = 21
			require.Error(t, db.Table(deploymentPrefix+"agents_inbox_events").Create(&row).Error)
		})
	}
}
func TestAgentsTeamLoopMigrationPreservesNullableScopeAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&agents.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Reminder{}, deploymentPrefix+"agents_reminders")
			if engine.name == "mysql" {
				return
			} // DDL protocol engine validates schema; row semantics require actual engine.
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 123000000, time.UTC)
			team := model.Reminder{ID: 1, TenantID: 1, AgentPrincipalID: 7, ThreadID: 4, Title: "Team loop", Checks: "Check", Escalation: "Escalate", OutputFormat: "CHECK_RESULT", State: "ACTIVE", MaxFires: 1, Version: 1, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Create(&team).Error)
			project := team
			project.ID = 2
			project.ProjectID = new(guid.ID(3))
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Create(&project).Error)
			invalid := team
			invalid.ID = 3
			invalid.ProjectID = new(guid.ID(0))
			require.Error(t, db.Table(deploymentPrefix+"agents_reminders").Create(&invalid).Error)
			var got model.Reminder
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Where("id=?", team.ID).First(&got).Error)
			require.Nil(t, got.ProjectID)
			require.WithinDuration(t, now, got.CreatedAt, time.Millisecond)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 1), "down must preserve team history rather than invent a project")
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Where("id=?", team.ID).Delete(&model.Reminder{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 1))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			got = model.Reminder{}
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Where("id=?", project.ID).First(&got).Error)
			require.NotNil(t, got.ProjectID)
			require.Equal(t, guid.ID(3), *got.ProjectID)
		})
	}
}

func TestAgentsReminderCalendarDowngradePreservesAnchorsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&agents.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			row := model.Reminder{ID: 91, TenantID: 1, AgentPrincipalID: 7, AnchorKind: "WORK_ITEM", WorkItemID: new(guid.ID(44)), ProjectID: new(guid.ID(3)), ScheduleKind: "PERIOD", PeriodKind: "DAY", Timezone: "America/Los_Angeles", Title: "Calendar", State: "ACTIVE", MaxFires: 5, Version: 1, CreatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Create(&row).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 3))
			var got model.Reminder
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Where("id = ?", row.ID).First(&got).Error)
			require.Equal(t, row.AnchorKind, got.AnchorKind)
			require.Equal(t, row.WorkItemID, got.WorkItemID)
			require.Equal(t, row.PeriodKind, got.PeriodKind)
			require.Equal(t, row.Timezone, got.Timezone)
			require.NoError(t, db.Table(deploymentPrefix+"agents_reminders").Where("id = ?", row.ID).Delete(&model.Reminder{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 3))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

// New protocol invariants only; previously landed runtime schemas are not rerun.
func TestAgentsRetirementProtocolAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&agents.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Retirement{}, deploymentPrefix+"agents_retirements")
			if engine.name == "mysql" {
				// The in-process engine checks DDL/model parity only; constraints require real MySQL 8.
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
			agent := guid.ID(7)
			row := model.Retirement{ID: 1, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "HANDOVER", Reason: "Transfer responsibility safely", AgentVersion: 2, Version: 1, CreatedBy: 2, CreatedAt: now, StepStartedAt: now}
			table := deploymentPrefix + "agents_retirements"
			require.NoError(t, db.Table(table).Create(&row).Error)
			duplicate := row
			duplicate.ID = 2
			require.Error(t, db.Table(table).Create(&duplicate).Error)
			// The same principal in another tenant does not collide.
			duplicate.TenantID = 2
			require.NoError(t, db.Table(table).Create(&duplicate).Error)
			require.NoError(t, db.Table(table).Where("id = ?", row.ID).Updates(map[string]any{"active_agent_id": nil, "state": "CANCELLED", "completed_at": now}).Error)
			duplicate.ID = 3
			duplicate.TenantID = 1
			require.NoError(t, db.Table(table).Create(&duplicate).Error)
			// Several terminal records may retain history without owning the active key.
			duplicate.ID = 4
			duplicate.State = "CANCELLED"
			duplicate.ActiveAgentID = nil
			duplicate.CompletedAt = &now
			require.NoError(t, db.Table(table).Create(&duplicate).Error)
			var stored model.Retirement
			require.NoError(t, db.Table(table).Where("id = ?", duplicate.ID).First(&stored).Error)
			require.WithinDuration(t, now, stored.CreatedAt, time.Millisecond)
			invalid := duplicate
			invalid.ID = 5
			invalid.State = "IN_PROGRESS"
			invalid.CompletedAt = nil
			require.Error(t, db.Table(table).Create(&invalid).Error, "active progress cannot omit its unique key")
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 8))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestAgentsRetirementPrecheckAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&agents.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Retirement{}, deploymentPrefix+"agents_retirements")
			if engine.name == "mysql" {
				return
			} // In-process engine checks DDL only.
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 9))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := deploymentPrefix + "agents_retirements"
			at := time.Now().UTC().Truncate(time.Millisecond)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": guid.ID(91), "tenant_id": guid.ID(1), "agent_principal_id": guid.ID(7), "active_agent_id": guid.ID(7), "mode": "NORMAL", "state": "IN_PROGRESS", "step": "HANDOVER", "reason": "Historical retirement record", "agent_version": 2, "version": 1, "created_by": guid.ID(2), "created_at": at, "step_started_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var legacy model.Retirement
			require.NoError(t, db.Table(table).Where("id = ?", 91).First(&legacy).Error)
			require.Empty(t, legacy.Precheck.WorkItems)
			// Existing empty manifests stay explicitly empty; new source snapshots
			// preserve both ID/version fences and actual execution identity facts.
			snapshot := model.RetirementPrecheck{WorkItems: []model.RetirementWorkItem{{AssignmentID: 10, ProjectID: 20, WorkItemID: 30, WorkVersion: 4, AssignmentVersion: 2, Title: "Transfer actual work", State: "CLAIMED"}}, Executions: []model.RetirementExecution{{ID: 40, ProjectID: 20, WorkItemID: 30, State: "RUNNING", Version: 3}}}
			agent := guid.ID(8)
			row := model.Retirement{ID: 92, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "HANDOVER", Reason: "New sourced retirement record", Precheck: snapshot, AgentVersion: 2, Version: 1, CreatedBy: 2, CreatedAt: at, StepStartedAt: at}
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.Retirement
			require.NoError(t, db.Table(table).Where("id = ?", 92).First(&stored).Error)
			require.Equal(t, snapshot, stored.Precheck)
			// An older process can still insert its original column set after the
			// additive upgrade; the default must preserve unknown source facts.
			require.NoError(t, db.Table(table).Create(map[string]any{"id": guid.ID(93), "tenant_id": guid.ID(1), "agent_principal_id": guid.ID(9), "active_agent_id": guid.ID(9), "mode": "NORMAL", "state": "IN_PROGRESS", "step": "HANDOVER", "reason": "Old writer after additive upgrade", "agent_version": 2, "version": 1, "created_by": guid.ID(2), "created_at": at, "step_started_at": at}).Error)
			var oldWriter model.Retirement
			require.NoError(t, db.Table(table).Where("id = ?", 93).First(&oldWriter).Error)
			require.Nil(t, oldWriter.Precheck.WorkItems)
			require.Nil(t, oldWriter.Precheck.Executions)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 9))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}
