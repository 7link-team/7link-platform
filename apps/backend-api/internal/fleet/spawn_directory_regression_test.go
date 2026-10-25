package fleet

import (
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnDirectoryUsesCurrentTenantIdentityAndObservedModels(t *testing.T) {
	m, db := directoryHarness(t)
	at := time.Now().UTC()
	seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
	r := query.BindRuntimes(db, m.names)
	require.NoError(t, r.WithContext(t.Context()).Create(&model.MachineRuntime{ID: 50, TenantID: 7, MachineID: 2, Kind: "CODEX", Version: "1", ExecutablePath: "/private/bin", ModelsJSON: `["actual-model"]`, InspectedAt: at}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		identity, found, err := m.spawnAgentOn(t.Context(), tx, 7, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.EqualValues(t, 101, identity.PrincipalID)
		_, found, err = m.spawnAgentOn(t.Context(), tx, 8, 1)
		require.NoError(t, err)
		require.False(t, found)
		for _, input := range []struct {
			runtime, model string
			exists         bool
		}{{"CODEX", "actual-model", true}, {"CODEX", "unknown-model", false}, {"CLAUDE_CODE", "actual-model", false}} {
			found, err = m.spawnModelOn(t.Context(), tx, 7, 2, input.runtime, input.model)
			require.NoError(t, err)
			require.Equal(t, input.exists, found)
		}
		return nil
	}))
	_, _, err := m.spawnAgentOn(t.Context(), db, 7, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.spawnModelOn(t.Context(), db, 7, 2, "CODEX", "actual-model")
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestSpawnDirectoryNeverTreatsUnavailableInventoryAsInstalled(t *testing.T) {
	for _, fault := range []string{"agent source", "runtime source", "corrupt catalog", "revoked", "missing dependency"} {
		t.Run(fault, func(t *testing.T) {
			m, db := directoryHarness(t)
			at := time.Now().UTC()
			seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
			r := query.BindRuntimes(db, m.names)
			require.NoError(t, r.WithContext(t.Context()).Create(&model.MachineRuntime{ID: 50, TenantID: 7, MachineID: 2, Kind: "CODEX", Version: "1", ExecutablePath: "/private/bin", ModelsJSON: `{"invalid":"shape"}`, InspectedAt: at}))
			switch fault {
			case "agent source":
				require.NoError(t, db.Migrator().DropTable(m.names.Agents.String()))
			case "runtime source":
				require.NoError(t, db.Migrator().DropTable(m.names.Runtimes.String()))
			case "revoked":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 2).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			case "missing dependency":
				m = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if fault == "agent source" || fault == "missing dependency" {
					identity, found, err := m.spawnAgentOn(t.Context(), tx, 7, 1)
					require.False(t, found)
					require.Zero(t, identity)
					require.Error(t, err)
				}
				found, err := m.spawnModelOn(t.Context(), tx, 7, 2, "CODEX", "actual-model")
				require.False(t, found)
				if fault == "revoked" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				return nil
			})
			require.NoError(t, err)
		})
	}
}
