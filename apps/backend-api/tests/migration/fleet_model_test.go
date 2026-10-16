package migration_test

import (
	"errors"
	"fmt"
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

func TestFleetAgentModelsAndConstraintsAcrossEngines(t *testing.T) {
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
			for _, row := range []tableModel{model.Agent{}, model.AgentRole{}} {
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
