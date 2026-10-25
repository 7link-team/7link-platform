package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestWorkdirLifecycleGuardPreservesHumanAndInterruptedExecutionPins(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	for _, state := range []string{"CREATED", "RUNNING", "PAUSED", "INTERRUPTED", "SUCCEEDED", "FAILED", "CANCELLED"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		_, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.State, state)
		require.NoError(t, err)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			quiet, e := f.m.workdirQuietOn(t.Context(), tx, 7, v.MachineID, v.Plan.WorkdirID)
			require.NoError(t, e)
			require.Equal(t, state == "SUCCEEDED" || state == "FAILED" || state == "CANCELLED", quiet, state) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			for _, ref := range [][3]guid.ID{{8, v.MachineID, v.Plan.WorkdirID}, {7, v.MachineID + 1, v.Plan.WorkdirID}, {7, v.MachineID, v.Plan.WorkdirID + 1}} {
				quiet, e = f.m.workdirQuietOn(t.Context(), tx, ref[0], ref[1], ref[2])
				require.NoError(t, e)
				require.True(t, quiet)
			}
			return nil
		}))
	}
	_, err := f.m.workdirQuietOn(t.Context(), f.db, 7, v.MachineID, v.Plan.WorkdirID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"state": "INTERRUPTED", "plan_json": "{}"})
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		quiet, e := f.m.workdirQuietOn(t.Context(), tx, 7, v.MachineID, v.Plan.WorkdirID)
		require.ErrorIs(t, e, ErrUnavailable)
		require.False(t, quiet)
		return nil
	}))
}

func TestBindWorkdirLifecycleRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindWorkdirLifecycle(module.NewServices()), module.ErrServicesProviding)
}
