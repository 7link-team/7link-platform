package migration_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFleetTerminalModelsAndActiveExecutionAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "terminal_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []tableModel{model.TerminalSession{}, model.TerminalAccess{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, source.Prefix+row.TableName())
				assertTableExists(t, raw, engine.dialect, row.TableName(), false)
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			access := source.Prefix + (model.TerminalAccess{}).TableName()
			sessions := source.Prefix + (model.TerminalSession{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			// Omitted optional policy columns exercise migration defaults as the
			// policy evolves; lifecycle defaults never imply Human authorization.
			require.NoError(t, db.Table(access).Create(map[string]any{"machine_id": 501, "tenant_id": 7, "enabled": false, "version": 1, "updated_at": at}).Error)
			var policy model.TerminalAccess
			require.NoError(t, db.Table(access).Where("machine_id = ?", 501).Take(&policy).Error)
			require.False(t, policy.Enabled)
			require.Equal(t, 1, policy.Version)
			require.Equal(t, 20000, policy.TunnelBandwidthKbps)
			require.Equal(t, 256, policy.TunnelMaxStreams)
			require.WithinDuration(t, at, policy.UpdatedAt, time.Millisecond)
			require.NoError(t, db.Table(access).Where("machine_id = ?", 501).Updates(map[string]any{"enabled": true, "version": 2}).Error)
			require.NoError(t, db.Table(access).Where("machine_id = ?", 501).Take(&policy).Error)
			require.True(t, policy.Enabled)
			require.Equal(t, 2, policy.Version)
			require.Error(t, db.Table(access).Create(map[string]any{"machine_id": 501, "tenant_id": 8, "enabled": false, "version": 1, "updated_at": at}).Error, "a machine has exactly one access policy")
			require.Error(t, db.Table(access).Where("machine_id = ?", 501).Update("machine_id", nil).Error, "access policy identity cannot become NULL")

			row := model.TerminalSession{ID: 601, TenantID: 7, MachineID: 501, ExecutionID: 701, ProjectID: 801, OpenedBy: 901, RunnerSessionID: 1001, ConnEpoch: 2, DispatchEpoch: 3, State: "OPENING", Workspace: "/registered/source", Sandboxed: false, OpenedAt: at, LastInputAt: at, ExpiresAt: at.Add(2 * time.Hour), RecordingChunks: "[]"}
			execution := row.ExecutionID
			row.ActiveExecution = &execution
			require.NoError(t, db.Table(sessions).Create(&row).Error)
			var stored model.TerminalSession
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Take(&stored).Error)
			require.NotNil(t, stored.ActiveExecution)
			require.Equal(t, row.ExecutionID, *stored.ActiveExecution)
			require.Equal(t, row.ConnEpoch, stored.ConnEpoch)
			require.Equal(t, row.DispatchEpoch, stored.DispatchEpoch)
			require.Equal(t, row.Workspace, stored.Workspace)
			require.False(t, stored.Sandboxed)
			require.Nil(t, stored.ClosedAt)
			require.Nil(t, stored.RecordingFileID)
			require.JSONEq(t, "[]", stored.RecordingChunks)
			require.WithinDuration(t, row.ExpiresAt, stored.ExpiresAt, time.Millisecond)
			require.Error(t, db.Table(sessions).Where("id = ?", row.ID).Update("id", nil).Error, "terminal session identity cannot become NULL")
			duplicate := row
			duplicate.ID = 602
			duplicate.MachineID = 502
			duplicate.OpenedBy = 902
			require.Error(t, db.Table(sessions).Create(&duplicate).Error, "one active terminal per tenant/execution, even on another machine or Human")
			duplicate.TenantID = 8
			require.NoError(t, db.Table(sessions).Create(&duplicate).Error, "active-execution uniqueness is tenant scoped")
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Updates(map[string]any{"active_execution": nil, "state": "CLOSED", "closed_at": at, "close_reason": "USER"}).Error)
			var closed model.TerminalSession
			require.NoError(t, db.Table(sessions).Where("id = ?", row.ID).Take(&closed).Error)
			require.Nil(t, closed.ActiveExecution)
			require.Equal(t, "CLOSED", closed.State)
			require.NotNil(t, closed.ClosedAt)
			duplicate.ID, duplicate.TenantID = 603, 7
			require.NoError(t, db.Table(sessions).Create(&duplicate).Error, "closing releases the active-execution lock")
			closed.ID = 604
			require.NoError(t, db.Table(sessions).Create(&closed).Error, "multiple historical sessions with NULL active_execution remain valid")
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect), "terminal migration rerun is idempotent")
		})
	}
}

func TestFleetAgentModelsAndConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			for _, row := range []tableModel{model.AgentMachineCommand{}, model.EnsemblePlan{}, model.EnsembleBranch{}, model.Agent{}, model.AgentRole{}, model.Machine{}, model.RunnerSession{}, model.MachineRuntime{}, model.Workdir{}, model.SlotReservation{}, model.MachineTool{}, model.ModelPrice{}, model.Enrollment{}, model.Inspection{}, model.CredentialRotation{}, model.MachineCredential{}, model.GatewayConfig{}, model.GatewayKey{}, model.GatewayRequest{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, deploymentPrefix+row.TableName())
			}
			if engine.dialect == "mysql" {
				return // In-memory MySQL does not prove real CHECK/generated-column behavior.
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			agents := deploymentPrefix + "fleet_agents"
			roles := deploymentPrefix + "fleet_agent_roles"
			now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
			insert := func(id int, changes map[string]any) error {
				row := map[string]any{"id": id, "tenant_id": 1, "principal_id": id + 100, "name": fmt.Sprintf("Agent %d", id), "handle": fmt.Sprintf("agent-%d", id), "avatar_seed": 1, "system_prompt": "Role instructions", "runtime_kind": "CODEX", "model_id": "test-model", "model_family": "test", "created_by_principal_id": 11, "owner_principal_id": 11, "created_at": now, "updated_at": now}
				for key, value := range changes {
					row[key] = value
				}
				return db.Table(agents).Create(row).Error
			}
			require.NoError(t, insert(1, nil))
			require.NoError(t, insert(20, map[string]any{"authorization_mode": "WORKSPACE_WRITE"}))
			require.Error(t, insert(21, map[string]any{"authorization_mode": "BYPASS"}))
			var first model.Agent
			require.NoError(t, db.Table(agents).Where("id = ?", 1).Take(&first).Error)
			require.Equal(t, "DRAFT", first.State)
			require.Equal(t, "HUMAN_CREATED", first.Origin)
			require.Equal(t, "NONE", first.ProcessStatus)
			require.True(t, first.AutoRestart)
			require.Equal(t, 24000, first.MaxContextTokens)
			require.JSONEq(t, `{"profile":"STANDARD"}`, first.MemoryPolicy)
			require.Nil(t, first.AdoptedAt)
			require.Error(t, insert(2, map[string]any{"name": "aGENT 1"}))
			require.NoError(t, insert(3, map[string]any{"name": "Agent 1", "tenant_id": 2}))
			require.Error(t, insert(4, map[string]any{"principal_id": 101}))
			require.NoError(t, db.Table(agents).Where("id = ?", 1).Update("state", "RETIRED").Error)
			require.NoError(t, insert(5, map[string]any{"name": "Agent 1"}))
			require.Error(t, insert(6, map[string]any{"handle": "agent-1"}), "retired handles cannot be reused")
			for _, invalid := range []map[string]any{{"origin": "AGENT_SPAWNED"}, {"avatar_seed": int64(9007199254740992)}, {"max_context_tokens": 3999}, {"max_parallel_items": 5}, {"state": "UNKNOWN"}, {"runtime_kind": "UNKNOWN"}} {
				require.Error(t, insert(9, invalid))
			}
			require.NoError(t, insert(10, map[string]any{"origin": "AGENT_SPAWNED", "parent_agent_id": 5, "spawn_grant_id": 8, "spawn_reason": "Execute independent work", "expires_at": time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)}))
			for _, role := range []string{"EXECUTOR", "REVIEWER", "COORDINATOR", "LEAD", "TESTER", "ACCEPTOR", "ASSISTANT"} {
				row := map[string]any{"tenant_id": 1, "agent_id": 5, "role": role, "iam_role_key": "wrong", "created_at": now, "updated_at": now}
				require.Error(t, db.Table(roles).Create(row).Error, "role mappings must not grant an unrelated role")
				row["iam_role_key"] = "agent_" + strings.ToLower(role)
				require.NoError(t, db.Table(roles).Create(row).Error)
			}

			fm := fleet.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix(deploymentPrefix)), fleet.Config{})
			require.NoError(t, db.Table(roles).Create(map[string]any{"tenant_id": 1, "agent_id": 10, "role": "EXECUTOR", "iam_role_key": "agent_executor", "created_at": now, "updated_at": now}).Error)
			abort := errors.New("rollback role revocation")
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				view, found, err := fm.ByPrincipalOn(t.Context(), tx, 1, 110)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, []string{"agent_executor"}, view.IAMRoleKeys)
				require.NoError(t, tx.Table(roles).Where("agent_id = ?", 10).Update("revoked_at", now).Error)
				view, found, err = fm.ByPrincipalOn(t.Context(), tx, 1, 110)
				require.NoError(t, err)
				require.True(t, found)
				require.Empty(t, view.IAMRoleKeys)
				return abort
			}), abort)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				view, found, err := fm.ByPrincipalOn(t.Context(), tx, 1, 110)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, []string{"agent_executor"}, view.IAMRoleKeys)
				return nil
			}))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect), "migration rerun is idempotent")
		})
	}
}

// These are migration/constraint fixtures, not evidence of a completed device
// enrollment or an actual runner execution. IAM provisioning owns those facts.
func TestFleetMachineSlotConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			if engine.dialect == "mysql" {
				return
			} // Real MySQL CHECK behavior needs the real-engine gate.
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
			key := "machine"
			machine := model.Machine{ID: 501, TenantID: 7, PrincipalID: 601, Name: "Machine", NameKey: &key, State: "OFFLINE", OS: "LINUX", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			machines := deploymentPrefix + machine.TableName()
			slots := deploymentPrefix + (model.SlotReservation{}).TableName()
			require.NoError(t, db.Table(machines).Create(&machine).Error)
			invalid := machine
			invalid.ID = 502
			invalid.PrincipalID = 602
			invalid.SlotCount = 9
			require.Error(t, db.Table(machines).Create(&invalid).Error)
			slot := 1
			first := model.SlotReservation{ID: 701, TenantID: 7, MachineID: 501, ExecutionID: 801, SlotNo: 1, ActiveSlot: &slot, CreatedAt: now}
			require.NoError(t, db.Table(slots).Create(&first).Error)
			second := first
			second.ID = 702
			second.ExecutionID = 802
			require.Error(t, db.Table(slots).Create(&second).Error, "active slot has exactly one holder")
			second.TenantID = 8
			require.Error(t, db.Table(slots).Create(&second).Error, "cross-tenant machine reference is rejected")
			require.Error(t, db.Table(slots).Where("id = ?", 701).Update("active_slot", nil).Error, "releasing the active lock without release time is invalid")
			require.NoError(t, db.Table(slots).Where("id = ?", 701).Updates(map[string]any{"active_slot": nil, "released_at": now}).Error)
			second.TenantID = 7
			require.NoError(t, db.Table(slots).Create(&second).Error, "released slot can be reused by a different execution")
			third := second
			third.ID = 703
			third.SlotNo = 2
			third.ActiveSlot = nil
			third.ReleasedAt = &now
			require.Error(t, db.Table(slots).Create(&third).Error, "one execution cannot silently acquire a second reservation")
		})
	}
}

func TestFleetResetCommandConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
			key := "reset-machine"
			machine := model.Machine{ID: 501, TenantID: 7, PrincipalID: 601, Name: "Machine", NameKey: &key, State: "OFFLINE", OS: "LINUX", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			require.NoError(t, db.Table(deploymentPrefix+machine.TableName()).Create(&machine).Error)
			row := model.ResetCommand{ID: 701, TenantID: 7, MachineID: 501, AgentPrincipalID: 801, SessionID: 901, ConnEpoch: 1, SessionEpoch: 2, WorkspaceEpoch: 1, Kind: "SESSION", State: "QUEUED", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
			table := deploymentPrefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.ResetCommand
			require.NoError(t, db.Table(table).First(&stored).Error)
			require.Equal(t, row.ID, stored.ID)
			require.Equal(t, row.ConnEpoch, stored.ConnEpoch)
			require.WithinDuration(t, row.ExpiresAt, stored.ExpiresAt, time.Millisecond)
			if engine.dialect != "mysql" { // Embedded MySQL does not enforce real CHECK/FK constraints.
				for _, mutate := range []func(*model.ResetCommand){func(r *model.ResetCommand) { r.Kind = "UNKNOWN" }, func(r *model.ResetCommand) { r.State = "SUCCEEDED" }, func(r *model.ResetCommand) { r.ConnEpoch = 0 }, func(r *model.ResetCommand) { r.SessionEpoch = 0 }, func(r *model.ResetCommand) { r.WorkspaceEpoch = 0 }, func(r *model.ResetCommand) { r.TenantID = 8 }} {
					invalid := row
					invalid.ID++
					mutate(&invalid)
					require.Error(t, db.Table(table).Create(&invalid).Error)
				}
			}
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestFleetEnsembleRecordsAcrossEngines(t *testing.T) {
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
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = "ensemble_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			for _, row := range []tableModel{model.EnsemblePlan{}, model.EnsembleBranch{}} {
				assertModelMatchesNamedTable(t, raw, engine.dialect, row, source.Prefix+row.TableName())
			}
			plans := source.Prefix + (model.EnsemblePlan{}).TableName()
			branches := source.Prefix + (model.EnsembleBranch{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			plan := model.EnsemblePlan{ID: 1, TenantID: 2, ProjectID: 3, WorkItemID: 4, State: "DRAFT", Reason: "Compare existing actual alternatives", ComparisonJSON: "[]", Version: 1, CreatedBy: 5, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(plans).Create(&plan).Error)
			branch := model.EnsembleBranch{ID: 10, TenantID: 2, ProjectID: 3, PlanID: 1, ExecutionID: 20}
			require.NoError(t, db.Table(branches).Create(&branch).Error)
			var stored model.EnsemblePlan
			require.NoError(t, db.Table(plans).Where("id = ?", 1).First(&stored).Error)
			require.WithinDuration(t, at, stored.CreatedAt, time.Millisecond)
			require.Nil(t, stored.ApprovalRequestedAt, "legacy creator is not an approval request")
			require.Nil(t, stored.ApprovedBy, "legacy creator is not an approver")
			require.Nil(t, stored.SelectedBy, "legacy creator is not a selector")
			require.NoError(t, db.Table(plans).Where("id = ?", 1).Updates(map[string]any{"approval_requested_at": at, "approved_by": 6, "approved_at": at, "selected_by": 7, "selected_at": at}).Error)
			require.NoError(t, db.Table(plans).Where("id = ?", 1).First(&stored).Error)
			require.EqualValues(t, 6, *stored.ApprovedBy)
			require.EqualValues(t, 7, *stored.SelectedBy)
			require.WithinDuration(t, at, *stored.ApprovedAt, time.Millisecond)
			duplicate := branch
			duplicate.ID = 11
			require.Error(t, db.Table(branches).Create(&duplicate).Error)
			if engine.name != "mysql" {
				for _, change := range []map[string]any{{"state": "UNKNOWN"}, {"version": 0}} {
					require.Error(t, db.Table(plans).Where("id = ?", 1).Updates(change).Error)
				}
				foreign := branch
				foreign.ID = 12
				foreign.ExecutionID = 21
				foreign.TenantID = 99
				require.Error(t, db.Table(branches).Create(&foreign).Error)
			}
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}

func TestFleetEnrollmentPollingAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = "polling_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 20))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.Enrollment{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 1, "tenant_id": 7, "client_id": 20, "device_hash": strings.Repeat("a", 64), "user_hash": strings.Repeat("b", 64), "name": "Machine", "os": "LINUX", "state": "PENDING", "expires_at": at.Add(time.Minute), "created_at": at, "updated_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Enrollment{}, table)
			var row model.Enrollment
			require.NoError(t, db.Table(table).Where("id = ?", 1).Take(&row).Error)
			require.EqualValues(t, 5, row.PollIntervalSeconds)
			require.Nil(t, row.LastPolledAt)
			require.NoError(t, db.Table(table).Where("id = ?", 1).Updates(map[string]any{"last_polled_at": at, "poll_interval_seconds": 10}).Error)
			require.NoError(t, db.Table(table).Where("id = ?", 1).Take(&row).Error)
			require.EqualValues(t, 10, row.PollIntervalSeconds)
			require.WithinDuration(t, at, *row.LastPolledAt, time.Millisecond)
			if engine.name != "mysql" {
				for _, invalid := range []int{0, 301} {
					require.Error(t, db.Table(table).Where("id = ?", 1).Update("poll_interval_seconds", invalid).Error)
				}
			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 20))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			row = model.Enrollment{}
			require.NoError(t, db.Table(table).Where("id = ?", 1).Take(&row).Error)
			require.EqualValues(t, 5, row.PollIntervalSeconds)
			require.Nil(t, row.LastPolledAt)
		})
	}
}

func TestFleetManualModelCatalogAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = "manual_"
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ManualModelCatalog{}, source.Prefix+(model.ManualModelCatalog{}).TableName())
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			machine := model.Machine{ID: 1, TenantID: 7, PrincipalID: 10, Name: "Machine", NameKey: new("machine"), State: "OFFLINE", OS: "LINUX", SlotCount: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+machine.TableName()).Create(&machine).Error)
			row := model.ManualModelCatalog{ID: 20, TenantID: 7, MachineID: 1, Runtime: "CODEX", ModelsJSON: `["proxy"]`, FamiliesJSON: `{"proxy":"google"}`, Version: 1, UpdatedAt: at}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			var stored model.ManualModelCatalog
			require.NoError(t, db.Table(table).Where("id = ?", 20).Take(&stored).Error)
			require.Equal(t, row.ModelsJSON, stored.ModelsJSON)
			require.Equal(t, row.FamiliesJSON, stored.FamiliesJSON)
			require.WithinDuration(t, at, stored.UpdatedAt, time.Millisecond)
			duplicate := row
			duplicate.ID = 21
			require.Error(t, db.Table(table).Create(&duplicate).Error)
			if engine.name != "mysql" {
				foreign := row
				foreign.ID = 22
				foreign.TenantID = 8
				require.Error(t, db.Table(table).Create(&foreign).Error)
				require.Error(t, db.Table(table).Where("id = ?", 20).Update("version", 0).Error)
			}
		})
	}
}

func TestFleetEnrollmentFactsPopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = "facts_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 22))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.Enrollment{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 1, "tenant_id": 7, "client_id": 20, "device_hash": strings.Repeat("a", 64), "user_hash": strings.Repeat("b", 64), "name": "Legacy machine", "os": "LINUX", "state": "PENDING", "expires_at": at.Add(time.Minute), "created_at": at, "updated_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Enrollment{}, table)
			var row model.Enrollment
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.Nil(t, row.FactsJSON)
			require.Equal(t, "Legacy machine", row.Name)
			require.WithinDuration(t, at, row.CreatedAt, time.Millisecond)
			facts := `{"hostname":"reported-host","arch":"amd64","runtimes":[{"kind":"CODEX","available":true,"version":"1"}]}`
			require.NoError(t, db.Table(table).Where("id=?", 1).Update("facts_json", facts).Error)
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.NotNil(t, row.FactsJSON)
			require.Equal(t, facts, *row.FactsJSON)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 22))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			row = model.Enrollment{}
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.Equal(t, "Legacy machine", row.Name)
			require.Nil(t, row.FactsJSON)
			require.WithinDuration(t, at, row.CreatedAt, time.Millisecond)
		})
	}
}

func TestFleetModelPriceCatalogPopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = "prices_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 23))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.ModelPrice{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 1, "tenant_id": 7, "runtime": "CODEX", "model": "legacy", "input_micros": int64(9007199254740993), "output_micros": 17, "updated_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ModelPrice{}, table)
			var row model.ModelPrice
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.Equal(t, int64(9007199254740993), row.InputMicros)
			require.Equal(t, 1, row.Version)
			require.Zero(t, row.CacheReadMicros)
			require.Zero(t, row.CacheWriteMicros)
			require.Nil(t, row.DeletedAt)
			require.NoError(t, db.Table(table).Where("id=?", 1).Updates(map[string]any{"cache_read_micros": int64(9007199254740995), "cache_write_micros": 19, "version": 2, "deleted_at": at}).Error)
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.Equal(t, int64(9007199254740995), row.CacheReadMicros)
			require.WithinDuration(t, at, *row.DeletedAt, time.Millisecond)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 23))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			row = model.ModelPrice{}
			require.NoError(t, db.Table(table).Where("id=?", 1).Take(&row).Error)
			require.Equal(t, int64(9007199254740993), row.InputMicros)
			require.Equal(t, 1, row.Version)
			require.Nil(t, row.DeletedAt)
		})
	}
}
