package delivery

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleHeavyPrincipalLockOrderIncludesOriginalInitiator(t *testing.T) {
	h, l := executionWorkFixture(t)
	services := module.NewServices()
	require.NoError(t, module.Provide[fleet.MachineDirectory](services, fleet.MachineDirectory{ByIDOn: func(_ context.Context, _ *gorm.DB, _ guid.ID, machine guid.ID) (fleet.MachineFact, error) {
		return fleet.MachineFact{ID: machine, PrincipalID: 100 + machine}, nil
	}}))
	require.NoError(t, services.Seal())
	h.m.executionHolds.PrincipalsOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
		require.Equal(t, l.TenantID, tenant)
		require.Equal(t, l.ProjectID, project)
		require.Equal(t, l.WorkItemID, work)
		return []guid.ID{97, 11, 9}, nil
	}
	locked := []guid.ID{}
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
	var runtime fleet.EnsembleRuntime
	require.NoError(t, h.m.bindEnsembleRuntime(services, fleet.EnsembleConnector{
		CheckBranchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
			return nil
		},
		BindRuntime: func(r fleet.EnsembleRuntime) error { runtime = r; return nil },
	}))
	branches := []fleet.EnsembleLaunchBranch{{PrincipalID: 91, MachineID: 1}, {PrincipalID: 92, MachineID: 2}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return runtime.LockOn(h.ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, branches)
	}))
	require.Equal(t, []guid.ID{9, 11, 91, 92, 97, 101, 102}, locked, "the original launcher and every participating principal are locked once in ascending order")
	h.m.executionHolds.PrincipalsOn = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return runtime.LockOn(h.ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, branches)
	}), ErrUnavailable)
}

func TestApprovedHeavyExecutionsKeepCurrentPointerEmptyAndDeferClaims(t *testing.T) {
	h, l := executionWorkFixture(t)
	a := query.BindAssignments(h.db, h.m.service.names)
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id = ?", l.AssignmentID).Updates(map[string]any{"ensemble_plan_id": 90, "active_slot": nil}).Error)
	second := l
	second.AssignmentID = 90000002
	second.ExecutionID = 90000003
	row, e := a.WithContext(h.ctx).Where(a.ID.Eq(l.AssignmentID)).First()
	require.NoError(t, e)
	row.ID = second.AssignmentID
	require.NoError(t, a.WithContext(h.ctx).Create(row))
	h.m.ensembleBranchCheck = func(_ context.Context, _ *gorm.DB, tenant, project, work, plan, assignment, execution guid.ID) error {
		if tenant == l.TenantID && project == l.ProjectID && work == l.WorkItemID && plan == 90 && ((assignment == l.AssignmentID && execution == l.ExecutionID) || (assignment == second.AssignmentID && execution == second.ExecutionID)) {
			return nil
		}
		return ErrAssignmentHolder
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for _, branch := range []ExecutionWorkLink{l, second} {
			if e := h.m.authorizeExecutionWorkOn(h.ctx, tx, branch, "CREATE"); e != nil {
				return e
			}
			if e := h.m.startExecutionWorkOn(h.ctx, tx, branch); e != nil {
				return e
			}
		}
		return h.m.finishExecutionWorkOn(h.ctx, tx, l, ExecutionCompletion{State: "SUCCEEDED", HeadCommit: "owner retains actual pending evidence"})
	}))
	w := query.BindWorkItems(h.db, h.m.service.names)
	work, e := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).First()
	require.NoError(t, e)
	require.Equal(t, "IN_PROGRESS", work.State)
	require.Nil(t, work.CurrentExecutionID)
	require.Nil(t, work.CurrentClaimID)
	results := query.BindResultVersions(h.db, h.m.service.names)
	count, e := results.WithContext(h.ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	forged := l
	forged.ExecutionID = second.ExecutionID
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, forged, "CREATE") }), ErrAssignmentHolder)
	h.m.ensembleBranchCheck = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "DISPATCH") }), ErrAssignmentHolder)
}
