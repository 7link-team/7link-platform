package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestParallelEnsembleReservesEachBudgetAndRollsBackInsufficientTotal(t *testing.T) {
	for _, capMicros := range []int64{1, 1000} {
		t.Run(guid.ID(capMicros).String(), func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.AuthorizeOn = func(_ context.Context, _ *gorm.DB, l WorkLink, _ string) error {
				if l.AssignmentID != 6 && l.AssignmentID != 7 {
					return ErrForbidden
				}
				return nil
			}
			f.m.machines.ByIDOn = func(_ context.Context, _ *gorm.DB, _, id guid.ID) (fleet.MachineFact, error) {
				return fleet.MachineFact{ID: id, PrincipalID: 100 + id, State: "ONLINE"}, nil
			}
			f.m.machines.ResolveWorkspaceOn = func(_ context.Context, _ *gorm.DB, _, machine, _, _ guid.ID) (fleet.WorkspaceFact, error) {
				return fleet.WorkspaceFact{ID: machine + 100, Path: "/workspace/" + machine.String()}, nil
			}
			require.NoError(t, f.db.Exec("CREATE TABLE heavy_reservations(execution_id BIGINT PRIMARY KEY,cost BIGINT NOT NULL)").Error)
			f.m.work.BudgetOn = func(_ context.Context, tx *gorm.DB, _ WorkLink) (BudgetFact, error) {
				var reserved int64
				e := tx.Table("heavy_reservations").Select("COALESCE(SUM(cost),0)").Scan(&reserved).Error
				return BudgetFact{CapMicros: &capMicros, ReservedMicros: reserved}, e
			}
			f.m.work.ReserveBudgetOn = func(_ context.Context, tx *gorm.DB, l WorkLink, cost int64, known bool) error {
				require.True(t, known)
				require.Positive(t, cost)
				return tx.Exec("INSERT INTO heavy_reservations VALUES (?,?)", l.ExecutionID, cost).Error
			}
			first, second := validCreate(), validCreate()
			first.ensemblePlanID = 90
			first.ensembleExecutionID = 901
			second.ensemblePlanID = 90
			second.ensembleExecutionID = 902
			second.AssignmentID = 7
			second.MachineID = 2
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.CreateOn(f.human, tx, 7, first)
				return err
			}), ErrForbidden, "ordinary create cannot opt into the parallel capability")
			var out []ExecutionView
			e := f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				out, e = f.m.parallelEnsembleOn(f.human, tx, 7, []ExecutionCreate{first, second})
				return e
			})
			var count int64
			require.NoError(t, f.db.Table("heavy_reservations").Count(&count).Error)
			if capMicros == 1 {
				require.ErrorIs(t, e, ErrBudgetExceeded)
				require.Zero(t, count)
				q := query.BindExecutions(f.db, f.m.names)
				n, e := q.WithContext(f.human).Count()
				require.NoError(t, e)
				require.Zero(t, n)
			} else {
				require.NoError(t, e)
				require.Equal(t, int64(2), count)
				require.Len(t, out, 2)
				require.Equal(t, "ENSEMBLE", out[0].BranchReason)
				require.Equal(t, guid.ID(902), out[1].ID)
			}
			first.ensemblePlanID = 0
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.parallelEnsembleOn(f.human, tx, 7, []ExecutionCreate{first, second})
				return e
			}))
		})
	}
}

func TestEnsembleCompletionPersistsActualCandidateAtomicallyAndReplays(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Update("branch_reason", "ENSEMBLE").Error)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	head := strings.Repeat("b", 40)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Observed branch head", Content: head})
	require.NoError(t, err)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: head, Usage: &ExecutionUsage{InputTokens: 40, OutputTokens: 20}}
	f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return ErrCapabilityUnavailable }
	_, err = f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrCapabilityUnavailable)
	rolledBack := f.read(t, v.ID)
	require.Equal(t, "RUNNING", rolledBack.State)
	require.Empty(t, rolledBack.PendingClaim, "candidate and terminal receipt roll back together")
	completed := 0
	f.m.work.FinishedOn = func(_ context.Context, _ *gorm.DB, l WorkLink, r WorkResult) error {
		completed++
		require.Equal(t, v.ID, l.ExecutionID)
		require.Equal(t, "ENSEMBLE_CANDIDATE_READY", r.Reason)
		require.Equal(t, head, r.HeadCommit)
		require.NotNil(t, r.ActualMicros, "known actual branch cost still reaches the existing settlement adapter")
		return nil
	}
	_, err = f.receipt(v.ID, in)
	require.NoError(t, err)
	actual := f.read(t, v.ID)
	require.Equal(t, "SUCCEEDED", actual.State)
	require.Equal(t, "ENSEMBLE_CANDIDATE_READY", actual.StateReason)
	require.Nil(t, actual.Claim, "an unselected candidate is not a submitted Claim")
	var pending ensembleCandidate
	require.NoError(t, json.Unmarshal(actual.PendingClaim, &pending))
	require.Equal(t, head, pending.HeadCommit)
	require.Len(t, pending.Evidence, 1)
	require.Equal(t, "COMMIT", pending.Evidence[0].Kind)
	require.Equal(t, "Observed branch head", pending.Evidence[0].Title)
	replay, err := f.receipt(v.ID, in)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, 1, completed, "replayed completion must not submit or settle twice")
}

func TestEnsembleRuntimeCancelsLeasedAndUnleasedBranchesWithRealReceipts(t *testing.T) {
	f := executionHarness(t)
	f.m.work.AuthorizeOn = func(_ context.Context, _ *gorm.DB, l WorkLink, _ string) error {
		if !f.authorized || l.AssignmentID != 6 && l.AssignmentID != 7 {
			return ErrForbidden
		}
		return nil
	}
	f.m.machines.ByIDOn = func(_ context.Context, _ *gorm.DB, _, id guid.ID) (fleet.MachineFact, error) {
		return fleet.MachineFact{ID: id, PrincipalID: 100 + id, State: "ONLINE"}, nil
	}
	f.m.machines.LockOn = f.m.machines.ByIDOn
	f.m.machines.ResolveWorkspaceOn = func(_ context.Context, _ *gorm.DB, _, machine, _, _ guid.ID) (fleet.WorkspaceFact, error) {
		return fleet.WorkspaceFact{ID: machine + 100, Path: "/workspace/" + machine.String()}, nil
	}
	var runtime fleet.EnsembleRuntime
	check := func(_ context.Context, _ *gorm.DB, tenant, project, work, plan, assignment, execution guid.ID) error {
		if tenant != 7 || project != 9 || work != 3 || plan != 90 || !(assignment == 6 && execution == 901 || assignment == 7 && execution == 902) {
			return ErrForbidden
		}
		return nil
	}
	require.NoError(t, f.m.bindEnsembleRuntime(fleet.EnsembleConnector{CheckBranchOn: check, BindRuntime: func(r fleet.EnsembleRuntime) error { runtime = r; return nil }}))
	first, second := validCreate(), validCreate()
	second.AssignmentID, second.MachineID = 7, 2
	firstPlan, err := json.Marshal(first.Plan)
	require.NoError(t, err)
	secondPlan, err := json.Marshal(second.Plan)
	require.NoError(t, err)
	branches := []fleet.EnsembleLaunchBranch{{PrincipalID: 11, MachineID: 1, AssignmentID: 6, ExecutionID: 901, Plan: firstPlan}, {PrincipalID: 12, MachineID: 2, AssignmentID: 7, ExecutionID: 902, Plan: secondPlan}}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return runtime.LaunchOn(f.human, tx, 7, 9, 3, 90, branches) }))
	f.pull(t, 901)
	_, err = f.receipt(901, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	f.authorized = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return runtime.AuthorizeOn(f.human, tx, 7, 9, 3, 90, branches, true) }))
	require.Nil(t, f.read(t, 901).CancelRequestedAt)
	f.authorized = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return runtime.CancelOn(f.human, tx, 7, 9, 3, 90, branches) }))
	leased, unleased := f.read(t, 901), f.read(t, 902)
	require.Equal(t, "RUNNING", leased.State, "cancellation intent does not fabricate runner acknowledgement")
	require.NotNil(t, leased.CancelRequestedAt)
	require.Equal(t, StateCanceled, unleased.State)
	require.Equal(t, "CONFIRMED", unleased.ResultState, "an unleased command has proved no runtime usage")
	_, err = f.receipt(901, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: StateCanceled})
	require.NoError(t, err)
	require.Equal(t, StateCanceled, f.read(t, 901).State)
	var active int64
	require.NoError(t, f.db.Table("slots").Where("active = ?", 1).Count(&active).Error)
	require.Zero(t, active)
}

func TestEnsembleRuntimeRejectsChangedBranchAuthorityAndMalformedPlans(t *testing.T) {
	for _, fault := range []string{"branch_check", "json", "missing", "project", "work", "assignment", "ordinary", "authority_missing", "authority", "terminal"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("branch_reason", "ENSEMBLE").Error)
			var runtime fleet.EnsembleRuntime
			checkErr := error(nil)
			if fault == "branch_check" {
				checkErr = ErrConflict
			}
			require.NoError(t, f.m.bindEnsembleRuntime(fleet.EnsembleConnector{CheckBranchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
				return checkErr
			}, BindRuntime: func(r fleet.EnsembleRuntime) error { runtime = r; return nil }}))
			b := fleet.EnsembleLaunchBranch{ExecutionID: v.ID, AssignmentID: 6, MachineID: 1, Plan: json.RawMessage("{")}
			if fault == "missing" {
				b.ExecutionID = 999
			}
			if fault == "project" {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("project_id", 99).Error)
			}
			if fault == "work" {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("work_item_id", 99).Error)
			}
			if fault == "assignment" {
				b.AssignmentID = 99
			}
			if fault == "ordinary" {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("branch_reason", "ROOT").Error)
			}
			if fault == "authority_missing" {
				f.m.work.AuthorizeOn = nil
			}
			if fault == "authority" {
				f.authorized = false
			}
			if fault == "terminal" {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "SUCCEEDED").Error)
			}
			before := f.read(t, v.ID)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "json" {
					return runtime.LaunchOn(f.human, tx, 7, 9, 3, 90, []fleet.EnsembleLaunchBranch{b})
				}
				if fault == "authority_missing" || fault == "authority" {
					return runtime.AuthorizeOn(f.human, tx, 7, 9, 3, 90, []fleet.EnsembleLaunchBranch{b}, true)
				}
				return runtime.CancelOn(f.human, tx, 7, 9, 3, 90, []fleet.EnsembleLaunchBranch{b})
			})
			if fault == "terminal" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, f.read(t, v.ID))
		})
	}
}
