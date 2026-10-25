package governance

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestSpawnUpdateTighteningBroadeningCASAndRollback(t *testing.T) {
	m, db, ctx, pub, in := spawnGrantHarness(t)
	in.MaxActiveAgents = 3
	grant, e := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, e)
	proofs := 0
	m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { proofs++; return nil }
	update := func(v int, in SpawnGrantUpdate) (SpawnGrantView, error) {
		var out SpawnGrantView
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.updateSpawnGrantOn(ctx, tx, 1, grant.ID, v, in, "cookie")
			return e
		})
		return out, e
	}
	two := 2
	out, e := update(1, SpawnGrantUpdate{MaxActiveAgents: &two, Reason: "Tighten unused available capacity"})
	require.NoError(t, e)
	require.Zero(t, proofs)
	require.Equal(t, 2, out.Version)
	three := 3
	pub.Fail = true
	_, e = update(2, SpawnGrantUpdate{MaxActiveAgents: &three, Reason: "Expand available worker capacity"})
	require.Error(t, e)
	pub.Fail = false
	out, e = update(2, SpawnGrantUpdate{MaxActiveAgents: &three, Reason: "Expand available worker capacity"})
	require.NoError(t, e)
	require.Equal(t, 2, proofs)
	require.Equal(t, 3, out.Version)
	_, e = update(2, SpawnGrantUpdate{Reason: "Stale update must be rejected"})
	require.ErrorIs(t, e, ErrVersion)
	ends := grant.EndsAt.Add(time.Hour)
	_, e = update(3, SpawnGrantUpdate{EndsAt: &ends, Reason: "Extend bounded capacity window"})
	require.NoError(t, e)
	require.Equal(t, 3, proofs)
	require.EqualValues(t, 4, seedCount(t, db, "event_probe"), "failed outbox emits no partial mutation")
}

func TestSpawnScopeBroadeningAndPartialUpdatePreserveBounds(t *testing.T) {
	_, _, _, _, in := spawnGrantHarness(t)
	old := in.SpawnGrantScope
	require.False(t, spawnScopeBroadened(old, old))
	newScope := old
	newScope.ProjectIDs = []guid.ID{}
	require.True(t, spawnScopeBroadened(old, newScope))
	newScope = old
	newScope.AllowedModels = []SpawnAllowedModel{{Runtime: "CODEX", Model: "other"}}
	require.True(t, spawnScopeBroadened(old, newScope))
	newScope = old
	newScope.AllowedMachineIDs = []guid.ID{6}
	require.True(t, spawnScopeBroadened(old, newScope))
	newScope = old
	newScope.AllowedRoles = []string{"COORDINATOR"}
	require.True(t, spawnScopeBroadened(old, newScope))
	maxActive := 10
	updated := applySpawnGrantUpdate(old, SpawnGrantUpdate{MaxActiveAgents: &maxActive})
	require.Equal(t, 10, updated.MaxActiveAgents)
	require.Equal(t, old.BudgetCapMicros, updated.BudgetCapMicros)
}
