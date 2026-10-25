package governance

import (
	"testing"
	"time"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnUpdateAllBoundsPersistAndFailurePreservesGrant(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	s := in.SpawnGrantScope
	update := SpawnGrantUpdate{ProjectIDs: &s.ProjectIDs, MaxActiveAgents: &s.MaxActiveAgents, MaxSpawnsPerDay: &s.MaxSpawnsPerDay, AllowedRoles: &s.AllowedRoles, AllowedMachineIDs: &s.AllowedMachineIDs, AllowedModels: &s.AllowedModels, MaxEffort: &s.MaxEffort, BudgetCapMicros: &s.BudgetCapMicros, PerAgentCapMicros: &s.PerAgentCapMicros, AgentTTLHours: &s.AgentTTLHours, IdleRetireHours: &s.IdleRetireHours, ApprovalMode: &s.ApprovalMode, Reason: "Preserve every bounded grant field"}
	var out SpawnGrantView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.updateSpawnGrantOn(ctx, tx, 1, grant.ID, 1, update, "cookie")
		return e
	}))
	require.Equal(t, s, out.SpawnGrantScope)
	require.Equal(t, 2, out.Version)
	for _, fault := range []string{"invalid reason", "missing", "cross tenant", "expired window", "corrupt scope", "quota", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, pub, in := spawnGrantHarness(t)
			grant, err := grantSpawn(t, m, db, ctx, in)
			require.NoError(t, err)
			id, tenant := grant.ID, guid.ID(1)
			u := SpawnGrantUpdate{Reason: "Keep the original grant on failure"}
			switch fault {
			case "invalid reason":
				u.Reason = "short"
			case "missing":
				id = 999
			case "cross tenant":
				tenant = 2
			case "expired window":
				end := m.seed.now().Add(-time.Hour)
				u.EndsAt = &end
			case "corrupt scope":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id = ?", id).Update("scope_json", "broken").Error)
			case "quota":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id = ?", id).Update("active_count", 2).Error)
			case "outbox":
				pub.Fail = true
			}
			err = db.Transaction(func(tx *gorm.DB) error { _, e := m.updateSpawnGrantOn(ctx, tx, tenant, id, 1, u, "cookie"); return e })
			require.Error(t, err)
			q := query.BindSpawnGrants(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
			require.NoError(t, err)
			require.Equal(t, 1, row.Version)
			require.Equal(t, "ACTIVE", row.State)
			require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
		})
	}
}

func TestSpawnMutationRequiresTransactionPrincipalLockAndCurrentVersion(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	_, err = m.lockSpawnGrantOn(ctx, nil, 1, grant.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.lockSpawnGrantOn(ctx, db, 1, grant.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.revokeSpawnGrantOn(ctx, tx, 1, grant.ID, 0, "Invalid expected version")
		return e
	}), ErrSpawnScope)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.revokeSpawnGrantOn(ctx, tx, 1, grant.ID, 2, "Stale expected grant version")
		return e
	}), ErrVersion)
	m.principals.Lock = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.lockSpawnGrantOn(ctx, tx, 1, grant.ID); return e }), ErrUnavailable)
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
}
