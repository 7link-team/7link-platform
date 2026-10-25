package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRebindFenceUsesFrozenExecutorAndFailsClosedOnCorruption(t *testing.T) {
	f, v := providerExecution(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		quiet, err := f.m.agentRebindQuietOn(t.Context(), tx, 7, 21)
		require.NoError(t, err)
		require.False(t, quiet)
		for _, agent := range []int64{11, 101, 22} {
			quiet, err = f.m.agentRebindQuietOn(t.Context(), tx, 7, guid.ID(agent))
			require.NoError(t, err)
			require.True(t, quiet)
		}
		quiet, err = f.m.agentRebindQuietOn(t.Context(), tx, 8, 21)
		require.NoError(t, err)
		require.True(t, quiet)
		return nil
	}))
	_, err := f.m.agentRebindQuietOn(t.Context(), f.db, 7, 21)
	require.ErrorIs(t, err, ErrInvalidArgument)
	q := query.BindExecutions(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.PlanJSON, "{}")
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		quiet, e := f.m.agentRebindQuietOn(t.Context(), tx, 7, 21)
		require.ErrorIs(t, e, ErrUnavailable)
		require.False(t, quiet)
		return nil
	}))
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.State, "SUCCEEDED")
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		quiet, e := f.m.agentRebindQuietOn(t.Context(), tx, 7, 21)
		require.NoError(t, e)
		require.True(t, quiet)
		return nil
	}))
}

func TestBindAgentRebindRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindAgentRebind(module.NewServices()), module.ErrServicesProviding)
}
