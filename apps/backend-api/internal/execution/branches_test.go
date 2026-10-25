package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func finishBranchSource(t *testing.T, f *executionFixture, v ExecutionView) {
	t.Helper()
	f.pull(t, v.ID)
	_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Actual commit", Content: strings.Repeat("b", 40)})
	require.NoError(t, e)
	_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: strings.Repeat("b", 40)})
	require.NoError(t, e)
}
func TestStrategyBranchFrozenBaselineHistoryAndActualDispatch(t *testing.T) {
	f := executionHarness(t)
	root := f.create(t)
	finishBranchSource(t, f, root)
	before := f.read(t, root.ID)
	in := StrategyBranch{Reason: "Alternative implementation", Objective: "Try the alternative"}
	var child ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		child, e = f.m.BranchStrategyOn(f.human, tx, 7, root.ID, before.Version, in)
		return e
	}))
	require.Equal(t, "STRATEGY", child.BranchReason)
	require.Equal(t, "BASELINE", child.StartPoint)
	require.Equal(t, root.ID, *child.ParentExecutionID)
	require.Equal(t, root.Plan.BaseCommit, child.Plan.BaseCommit)
	require.Equal(t, in.Objective, child.Plan.Objective)
	require.Equal(t, in.Reason, child.FeedbackSnapshot)
	require.NotEqual(t, root.PlanHash, child.PlanHash)
	require.Nil(t, child.SourceDecisionID)
	require.Nil(t, child.FullAutoGrantID)
	require.Equal(t, before, f.read(t, root.ID))
	f.pull(t, child.ID)
	_, e := f.receipt(child.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	var first, next ExecutionPage
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = f.m.BranchHistoryOn(f.human, tx, 7, root.ID, 0, 1)
		return e
	}))
	require.True(t, first.HasMore)
	require.Equal(t, root.ID, first.Items[0].ID)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		next, e = f.m.BranchHistoryOn(f.human, tx, 7, root.ID, root.ID, 1)
		return e
	}))
	require.False(t, next.HasMore)
	require.Equal(t, child.ID, next.Items[0].ID)
	// A quality continuation starts at parent HEAD. A later strategy still uses root baseline.
	var source model.Execution
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", child.ID).First(&source).Error)
	plan := child.Plan
	plan.BaseCommit = strings.Repeat("b", 40)
	source.PlanJSON = mustBranchPlan(t, plan)
	source.PlanHash = hash([]byte(source.PlanJSON))
	source.BranchReason = "QUALITY"
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", source.ID).Updates(map[string]any{"plan_json": source.PlanJSON, "plan_hash": source.PlanHash, "branch_reason": "QUALITY", "strategy_parent_id": nil, "parent_execution_id": root.ID, "source_decision_id": 91, "state": "SUCCEEDED"}).Error)
	require.NoError(t, f.db.Table("slots").Where("execution_id=?", child.ID).Update("active", 0).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		v, e := f.m.BranchStrategyOn(f.human, tx, 7, child.ID, f.read(t, child.ID).Version, in)
		if e == nil {
			require.Equal(t, root.Plan.BaseCommit, v.Plan.BaseCommit)
		}
		return e
	}))
}
func TestStrategyBranchDenialsAndAtomicRollback(t *testing.T) {
	for _, fault := range []string{"stale", "revoked", "tenant", "audit", "outbox", "ancestry", "purpose"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			root := f.create(t)
			finishBranchSource(t, f, root)
			root = f.read(t, root.ID)
			version := root.Version
			ctx := f.human
			switch fault {
			case "stale":
				version--
			case "revoked":
				f.authorized = false
			case "tenant":
				ctx = authn.WithClaims(context.Background(), &authn.Claims{TenantID: 8, PrincipalID: 11})
			case "audit":
				f.m.audit = nil
			case "outbox":
				f.pub.fail = true
			case "ancestry":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", root.ID).Updates(map[string]any{"parent_execution_id": root.ID, "source_decision_id": 92}).Error)
			case "purpose":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", root.ID).Update("branch_reason", "ENSEMBLE").Error)
			}
			var before, after, effectsBefore, effectsAfter int64
			require.NoError(t, f.db.Table("effects").Count(&effectsBefore).Error)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.BranchStrategyOn(ctx, tx, 7, root.ID, version, StrategyBranch{Reason: "Alternative", Objective: "Different approach"})
				return e
			})
			require.Error(t, err)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
			require.NoError(t, f.db.Table("effects").Count(&effectsAfter).Error)
			require.Equal(t, effectsBefore, effectsAfter)
		})
	}
	f := executionHarness(t)
	v := f.create(t)
	for _, limit := range []int{0, 101} {
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.BranchHistoryOn(f.human, tx, 7, v.ID, 0, limit); return e }))
	}
	f.authorized = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.BranchHistoryOn(f.human, tx, 7, v.ID, 0, 50); return e }))
}

func mustBranchPlan(t *testing.T, p ExecutionPlan) string {
	t.Helper()
	b, e := json.Marshal(p)
	require.NoError(t, e)
	return string(b)
}

func TestStrategyBranchRejectsCorruptAncestryAndCurrentAuthorityWithoutSideEffects(t *testing.T) {
	for _, fault := range []string{"reason", "machine", "version", "missing", "parent_plan", "root_plan", "root_missing", "root_scope", "root_commit", "repository", "machine_capability", "machine_error", "principal_lock", "authority", "history_query", "history_plan"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			root := f.create(t)
			finishBranchSource(t, f, root)
			root = f.read(t, root.ID)
			input := StrategyBranch{Reason: "Alternative implementation", Objective: "Independent implementation"}
			id, version := root.ID, root.Version
			failure := errors.New("branch dependency unavailable")
			switch fault {
			case "reason":
				input.Reason = ""
			case "machine":
				input.MachineID = -1
			case "version":
				version = 0
			case "missing":
				id++
			case "parent_plan", "history_plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("plan_json", "{").Error)
			case "root_missing", "root_scope", "root_plan", "root_commit", "repository":
				if fault == "root_missing" {
					require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"strategy_parent_id": guid.ID(999999), "branch_reason": "STRATEGY"}).Error)
					break
				}
				parent := model.Execution{}
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).First(&parent).Error)
				parent.ID += 10000
				parent.StrategyParentID = nil
				parent.ParentExecutionID = nil
				if fault == "root_scope" {
					parent.WorkItemID++
				}
				plan := root.Plan
				if fault == "root_plan" {
					parent.PlanJSON = "{"
				}
				if fault == "root_commit" {
					plan.BaseCommit = "bad"
					parent.PlanJSON = mustBranchPlan(t, plan)
				}
				if fault == "repository" {
					plan.RepositoryID++
					parent.PlanJSON = mustBranchPlan(t, plan)
				}
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&parent).Error)
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"strategy_parent_id": parent.ID, "branch_reason": "STRATEGY"}).Error)
			case "machine_capability":
				f.m.machines.ByIDOn = nil
			case "machine_error":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "principal_lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "authority":
				f.authorized = false
			case "history_query":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("branch_history_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Executions.String() && tx.Statement.Clauses["LIMIT"].Expression != nil {
						tx.AddError(failure)
					}
				}))
			}
			var count, effects int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&count).Error)
			require.NoError(t, f.db.Table("effects").Count(&effects).Error)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(fault, "history_") {
					_, e := f.m.BranchHistoryOn(f.human, tx, 7, id, 0, 10)
					return e
				}
				if strings.HasPrefix(fault, "machine_") || fault == "principal_lock" || fault == "authority" {
					_, e := f.m.strategyAuthorityOn(f.human, tx, 7, id)
					return e
				}
				_, e := f.m.BranchStrategyOn(f.human, tx, 7, id, version, input)
				return e
			})
			require.Error(t, err)
			if fault == "history_query" {
				require.NoError(t, f.db.Callback().Query().Remove("branch_history_failure"))
			}
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, count, after)
			require.NoError(t, f.db.Table("effects").Count(&after).Error)
			require.Equal(t, effects, after)
		})
	}
}

func TestStrategyBranchEveryStorageFailurePreservesSourceAndHistory(t *testing.T) {
	for _, command := range []string{"create", "history"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				root := f.create(t)
				finishBranchSource(t, f, root)
				root = f.read(t, root.ID)
				return f, func(tx *gorm.DB) error {
					if command == "history" {
						out, e := f.m.BranchHistoryOn(f.human, tx, 7, root.ID, 0, 10)
						if e == nil {
							require.Len(t, out.Items, 1)
							require.Equal(t, root.ID, out.Items[0].ID)
						}
						return e
					}
					out, e := f.m.BranchStrategyOn(f.human, tx, 7, root.ID, root.Version, StrategyBranch{Reason: "Alternative implementation", Objective: "Independent strategy"})
					if e == nil {
						require.Equal(t, root.Plan.BaseCommit, out.Plan.BaseCommit)
						require.Equal(t, root.ID, *out.ParentExecutionID)
					}
					return e
				}
			})
		})
	}
}
