package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnReconcileSourceFailurePreservesLiveGrant(t *testing.T) {
	for _, fault := range []string{"writer", "dependency", "query", "principal lock", "principal state"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			grant, err := grantSpawn(t, m, db, ctx, in)
			require.NoError(t, err)
			switch fault {
			case "writer":
				m.runtime = nil
			case "dependency":
				m.spawnDirectory.AgentOn = nil
			case "query":
				require.NoError(t, db.Migrator().RenameTable(m.seed.names.SpawnGrants.String(), "hidden_grants"))
			case "principal lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "principal state":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			}
			require.Error(t, m.reconcileSpawnGrants(ctx, jobs.Job{}))
			if fault == "query" {
				require.NoError(t, db.Migrator().RenameTable("hidden_grants", m.seed.names.SpawnGrants.String()))
			}
			var state string
			require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id = ?", grant.ID).Pluck("state", &state).Error)
			require.Equal(t, "ACTIVE", state)
			require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
			require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
		})
	}
}
