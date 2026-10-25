package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestExecutionHistoryUsesFrozenExecutorAndEveryBranch(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	at := time.Now().UTC().Add(time.Hour)
	q := query.BindExecutions(f.db, f.m.names)
	plan := v.Plan
	plan.ExecutorPrincipalID = 777
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.PlanJSON, string(raw))
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, err := f.m.dispatchHistoryOn(f.human, tx, 7, v.ProjectID, []guid.ID{v.WorkItemID}, at)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, guid.ID(777), rows[0].ExecutorPrincipalID)
		require.Equal(t, v.ID, rows[0].ID)
		rows, err = f.m.dispatchHistoryOn(f.human, tx, 7, v.ProjectID, []guid.ID{v.WorkItemID + 1}, at)
		require.NoError(t, err)
		require.Empty(t, rows)
		rows, err = f.m.dispatchHistoryOn(f.human, tx, 7, v.ProjectID, nil, at)
		require.NoError(t, err)
		require.Empty(t, rows)
		_, err = f.m.dispatchHistoryOn(f.human, tx, 8, v.ProjectID, []guid.ID{v.WorkItemID}, at)
		require.Error(t, err)
		return nil
	}))
	_, err = f.m.dispatchHistoryOn(f.human, f.db, 7, v.ProjectID, nil, at)
	require.ErrorIs(t, err, ErrInvalidArgument)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.dispatchHistoryOn(f.human, tx, 7, v.ProjectID, []guid.ID{v.WorkItemID}, at)
		return err
	}))
	s := module.NewServices()
	require.NoError(t, f.m.provideDispatchHistory(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[DispatchHistory](s)
	require.NoError(t, err)
	require.NotNil(t, p.ForWorkOn)
}

func TestProvideDispatchHistoryRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideDispatchHistory(s))
	require.ErrorIs(t, f.m.provideDispatchHistory(s), module.ErrServiceDuplicate)
}
