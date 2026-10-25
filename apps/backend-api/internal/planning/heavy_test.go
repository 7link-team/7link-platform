package planning

import (
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func heavyFixture(t *testing.T) (harness, HeavyCreate) {
	h := setup(t)
	h.m.heavyReference = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }
	h.m.heavyBudget = budget.Provider{FactOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (budget.Fact, error) {
		ceiling := int64(100)
		return budget.Fact{CapMicros: &ceiling}, nil
	}}
	b := HeavyBranch{CapMicros: "10", Execution: execution.ExecutionCreate{ProjectID: 3, WorkItemID: 100, AssignmentID: 11}}
	in := HeavyCreate{ProjectID: 3, WorkItemID: 100, Reason: "Compare actual alternatives", Branches: []HeavyBranch{b, b}}
	in.Branches[1].Execution.AssignmentID = 12
	return h, in
}
func TestHeavyDraftRequiresSourceReferencesBeforeWriting(t *testing.T) {
	h, in := heavyFixture(t)
	failure := errors.New("assignment not in current work scope")
	calls := 0
	h.m.heavyReference = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error {
		calls++
		if calls == 2 {
			return failure
		}
		return nil
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createHeavyOn(claims(t, 2), tx, 1, in); return err }), failure)
	require.Equal(t, 2, calls)
	require.Empty(t, in.Branches[0].State, "normalization must not mutate caller input")
	var n int64
	require.NoError(t, h.db.Table(h.m.names.HeavyRuns.String()).Count(&n).Error)
	require.Zero(t, n)
	h.m.heavyReference = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.createHeavyOn(claims(t, 2), tx, 1, in); return err }), ErrUnavailable)
}
func TestHeavyDraftBudgetsCurrentScopeUnavailableLaunchAndCancelIntent(t *testing.T) {
	h, in := heavyFixture(t)
	ctx := claims(t, 2)
	var v HeavyView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = h.m.createHeavyOn(ctx, tx, 1, in); return e }))
	require.Equal(t, "DRAFT", v.State)
	require.Equal(t, "10", v.Branches[0].CapMicros)
	require.Zero(t, v.Branches[0].ExecutionID)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.heavyOn(ctx, tx, 2, 3, v.ID, "work_item_read"); return e }), ErrForbidden)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeHeavyOn(ctx, tx, 1, 3, v.ID, 2, "LAUNCH"); return e }), ErrConflict)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.changeHeavyOn(ctx, tx, 1, 3, v.ID, 1, "LAUNCH")
		return e
	}))
	require.Equal(t, "UNAVAILABLE", v.State)
	require.Zero(t, v.Branches[0].ExecutionID, "ordinary single-work launcher cannot be asserted to launch alternatives")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.changeHeavyOn(ctx, tx, 1, 3, v.ID, 2, "CANCEL")
		return e
	}))
	require.Equal(t, "CANCEL_REQUESTED", v.State)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeHeavyOn(ctx, tx, 1, 3, v.ID, 3, "LAUNCH"); return e }), ErrConflict)
}
func TestHeavyLaunchCapFailureRollsBackAllCanonicalBranchWrites(t *testing.T) {
	h, in := heavyFixture(t)
	ctx := claims(t, 2)
	var v HeavyView
	require.NoError(t, h.db.Exec("CREATE TABLE branch_probe (id BIGINT)").Error)
	h.m.heavyCommands.ParallelOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, inputs []execution.ExecutionCreate) ([]execution.ExecutionView, error) {
		require.NoError(t, tx.Exec("INSERT INTO branch_probe VALUES (1)").Error)
		out := []execution.ExecutionView{}
		for i, b := range inputs {
			price := int64(1)
			if i == 1 {
				price = 11
			}
			out = append(out, execution.ExecutionView{ID: guid.ID(100 + i), ProjectID: 3, WorkItemID: 100, AssignmentID: b.AssignmentID, State: "CREATED", Version: 1, Plan: execution.ExecutionPlan{MaxInputTokens: 1000000, PriceSnapshot: &execution.ExecutionPrice{InputMicros: price}}})
		}
		return out, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = h.m.createHeavyOn(ctx, tx, 1, in); return e }))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeHeavyOn(ctx, tx, 1, 3, v.ID, 1, "LAUNCH"); return e }), budget.ErrExceeded)
	var n int64
	require.NoError(t, h.db.Table("branch_probe").Count(&n).Error)
	require.Zero(t, n)
	q := query.BindHeavyRuns(h.db, h.m.names)
	row, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, "DRAFT", row.State)
	require.Equal(t, 1, row.Version)
}
func TestHeavyDraftRejectsOverflowBudgetAndAtomicAuditFailure(t *testing.T) {
	for _, caps := range [][2]string{{"90", "90"}, {"9223372036854775807", "1"}, {"01", "1"}, {"-1", "1"}} {
		t.Run(caps[0], func(t *testing.T) {
			h, in := heavyFixture(t)
			in.Branches[0].CapMicros = caps[0]
			in.Branches[1].CapMicros = caps[1]
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createHeavyOn(claims(t, 2), tx, 1, in); return e }))
			var n int64
			require.NoError(t, h.db.Table(h.m.names.HeavyRuns.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
	h, in := heavyFixture(t)
	failure := errors.New("audit unavailable")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createHeavyOn(claims(t, 2), tx, 1, in); return e }), failure)
	var n int64
	require.NoError(t, h.db.Table(h.m.names.HeavyRuns.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestHeavyParallelLaunchAndCancellationKeepCanonicalVersionsAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "count", "scope", "price", "launch_failure", "cancel_missing", "cancel_failure", "audit"} {
		t.Run(mode, func(t *testing.T) {
			h, in := heavyFixture(t)
			failure := errors.New("canonical operation unavailable")
			require.NoError(t, h.db.Exec("CREATE TABLE branch_effect (id BIGINT)").Error)
			h.m.heavyCommands.ParallelOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, inputs []execution.ExecutionCreate) ([]execution.ExecutionView, error) {
				require.NoError(t, tx.Exec("INSERT INTO branch_effect VALUES (1)").Error)
				if mode == "launch_failure" {
					return nil, failure
				}
				out := make([]execution.ExecutionView, len(inputs))
				for i, b := range inputs {
					out[i] = execution.ExecutionView{ID: guid.ID(100 + i), ProjectID: 3, WorkItemID: 100, AssignmentID: b.AssignmentID, Version: 2, State: "CREATED", Plan: execution.ExecutionPlan{MaxInputTokens: 1000000, PriceSnapshot: &execution.ExecutionPrice{InputMicros: 1}}}
				}
				if mode == "count" {
					return out[:1], nil
				}
				if mode == "scope" {
					out[1].AssignmentID = 99
				}
				if mode == "price" {
					out[1].Plan.PriceSnapshot = nil
				}
				return out, nil
			}
			var v HeavyView
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = h.m.createHeavyOn(claims(t, 2), tx, 1, in); return e }))
			auditFn := h.m.audit
			if mode == "audit" {
				h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = h.m.changeHeavyOn(claims(t, 2), tx, 1, 3, v.ID, v.Version, "LAUNCH")
				return e
			})
			if mode == "count" || mode == "scope" || mode == "price" || mode == "launch_failure" || mode == "audit" {
				require.Error(t, err)
				var n int64
				require.NoError(t, h.db.Table("branch_effect").Count(&n).Error)
				require.Zero(t, n)
				row, e := query.BindHeavyRuns(h.db, h.m.names).WithContext(t.Context()).First()
				require.NoError(t, e)
				require.Equal(t, "DRAFT", row.State)
				require.Equal(t, 1, row.Version)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "LAUNCHED", v.State)
			require.Equal(t, 2, v.Version)
			for _, b := range v.Branches {
				require.Equal(t, 2, b.ExecutionVersion)
				require.Equal(t, "CREATED", b.State)
			}
			cancelCalls := 0
			if mode != "cancel_missing" {
				h.m.heavyCommands.CancelOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, id guid.ID) (execution.ExecutionView, error) {
					cancelCalls++
					require.NoError(t, tx.Exec("INSERT INTO branch_effect VALUES (2)").Error)
					if mode == "cancel_failure" {
						return execution.ExecutionView{}, failure
					}
					return execution.ExecutionView{ID: id, State: "CANCEL_REQUESTED", Version: 3}, nil
				}
			}
			original := v
			err = h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = h.m.changeHeavyOn(claims(t, 2), tx, 1, 3, original.ID, original.Version, "CANCEL")
				return e
			})
			if mode == "cancel_missing" || mode == "cancel_failure" {
				require.Error(t, err)
				row, e := query.BindHeavyRuns(h.db, h.m.names).WithContext(t.Context()).First()
				require.NoError(t, e)
				require.Equal(t, "LAUNCHED", row.State)
				require.Equal(t, 2, row.Version)
				var n int64
				require.NoError(t, h.db.Table("branch_effect").Count(&n).Error)
				require.EqualValues(t, 1, n)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "CANCEL_REQUESTED", v.State)
			require.Equal(t, 2, cancelCalls)
			for _, b := range v.Branches {
				require.Equal(t, 3, b.ExecutionVersion)
				require.Equal(t, "CANCEL_REQUESTED", b.State)
			}
			h.m.audit = auditFn
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				replay, e := h.m.changeHeavyOn(claims(t, 2), tx, 1, 3, v.ID, v.Version, "CANCEL")
				require.Equal(t, v.Version, replay.Version)
				return e
			}))
			require.Equal(t, 2, cancelCalls)
		})
	}
}

func TestHeavyDraftRejectsUnavailableBudgetAndIdentifierSources(t *testing.T) {
	for _, mode := range []string{"budget", "negative", "zero_id", "id_error", "insert"} {
		t.Run(mode, func(t *testing.T) {
			h, in := heavyFixture(t)
			switch mode {
			case "budget":
				h.m.heavyBudget.FactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (budget.Fact, error) {
					return budget.Fact{}, ErrUnavailable
				}
			case "negative":
				h.m.heavyBudget.FactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (budget.Fact, error) {
					return budget.Fact{SpentMicros: -1}, nil
				}
			case "zero_id":
				h.m.ids = failingPlanningIDs{}
			case "id_error":
				h.m.ids = failingPlanningIDs{err: ErrUnavailable}
			case "insert":
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("heavy_draft_insert", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.HeavyRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createHeavyOn(claims(t, 2), tx, 1, in); return e }), ErrUnavailable)
			require.NoError(t, h.db.Callback().Create().Remove("heavy_draft_insert"))
			var n int64
			require.NoError(t, h.db.Table(h.m.names.HeavyRuns.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
