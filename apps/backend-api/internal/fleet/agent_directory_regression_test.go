package fleet

import (
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchFactsRejectUnavailableSourcesWithoutPartialRecommendations(t *testing.T) {
	for _, source := range []string{"agents", "roles", "machines", "slots", "runtimes"} {
		t.Run(source, func(t *testing.T) {
			m, db := directoryHarness(t)
			at := time.Now().UTC()
			seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
			a := query.BindAgents(db, m.names)
			_, err := a.WithContext(t.Context()).Where(a.ID.Eq(guid.ID(1))).Update(a.MachineID, guid.ID(2))
			require.NoError(t, err)
			tables := map[string]string{"agents": m.names.Agents.String(), "roles": m.names.AgentRoles.String(), "machines": m.names.Machines.String(), "slots": m.names.Slots.String(), "runtimes": m.names.Runtimes.String()}
			require.NoError(t, db.Migrator().DropTable(tables[source]))
			err = db.Transaction(func(tx *gorm.DB) error {
				facts, err := m.dispatchFactsOn(t.Context(), tx, 7)
				require.Nil(t, facts, "a missing current source must never become a usable partial candidate list")
				return err
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tables[source])
		})
	}
}

func TestDispatchFactsRejectCorruptObservedRuntimeCatalog(t *testing.T) {
	m, db := directoryHarness(t)
	at := time.Now().UTC()
	seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &at)
	a := query.BindAgents(db, m.names)
	_, err := a.WithContext(t.Context()).Where(a.ID.Eq(guid.ID(1))).Update(a.MachineID, guid.ID(2))
	require.NoError(t, err)
	r := query.BindRuntimes(db, m.names)
	require.NoError(t, r.WithContext(t.Context()).Create(&model.MachineRuntime{ID: 50, TenantID: 7, MachineID: 2, Kind: "CODEX", Version: "1", ExecutablePath: "/private/bin", ModelsJSON: `{"unexpected":"object"}`, InspectedAt: at}))
	err = db.Transaction(func(tx *gorm.DB) error {
		facts, err := m.dispatchFactsOn(t.Context(), tx, 7)
		require.Nil(t, facts)
		return err
	})
	require.ErrorContains(t, err, "cannot unmarshal object")
}
