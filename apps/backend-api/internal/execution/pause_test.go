package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionHoldRequiresExactRunnerAcknowledgements(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
	require.ErrorIs(t, err, ErrConflict)
	change := func(release bool) ([]delivery.ExecutionHoldFact, error) {
		var facts []delivery.ExecutionHoldFact
		e := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			facts, e = f.m.changeHoldOn(f.human, tx, 7, 9, 3, release)
			return e
		})
		return facts, e
	}
	facts, err := change(false)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "PAUSE_REQUESTED", facts[0].Control)
	require.Equal(t, "RUNNING", f.read(t, v.ID).State)
	_, err = change(true)
	require.ErrorIs(t, err, ErrConflict)
	ack, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
	require.NoError(t, err)
	require.Equal(t, "PAUSED", ack.State)
	replay, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	facts, err = change(false)
	require.NoError(t, err)
	require.Equal(t, "PAUSED", facts[0].Control)
	facts, err = change(true)
	require.NoError(t, err)
	require.Equal(t, "RESUME_REQUESTED", facts[0].Control)
	require.Equal(t, "PAUSED", f.read(t, v.ID).State)
	facts, err = change(false)
	require.NoError(t, err)
	require.Equal(t, "PAUSED", facts[0].Control)
	// A resume already performed by the Runner remains a fact after a newer
	// hold. Rejecting it would permanently block the immutable receipt journal.
	resumed := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "RESUMED"}
	ack, err = f.receipt(v.ID, resumed)
	require.NoError(t, err)
	require.Equal(t, "RUNNING", ack.State)
	require.True(t, ack.PauseRequested)
	require.False(t, ack.ResumeRequested)
	require.Equal(t, "PAUSE_REQUESTED", f.read(t, v.ID).StateReason)
	replay, err = f.receipt(v.ID, resumed)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.True(t, replay.PauseRequested)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "PAUSED"})
	require.NoError(t, err)
	facts, err = change(false)
	require.NoError(t, err)
	require.Equal(t, "PAUSED", facts[0].Control)
	facts, err = change(true)
	require.NoError(t, err)
	require.Equal(t, "RESUME_REQUESTED", facts[0].Control)
	ack, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 5, Kind: "RESUMED"})
	require.NoError(t, err)
	require.Equal(t, "RUNNING", ack.State)
	require.False(t, ack.PauseRequested)
	f.sessionValid = false
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 6, Kind: "PAUSED"})
	require.ErrorIs(t, err, ErrFenced)
}

func TestExecutionHoldCannotResumeBudgetPauseOrLoseAtomicity(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	f.pub.fail = true
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, false); return e }))
	require.NotEqual(t, "PAUSE_REQUESTED", f.read(t, v.ID).StateReason)
	f.pub.fail = false
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"state": "PAUSED", "state_reason": "BUDGET_PAUSE"}).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		facts, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, true)
		require.Len(t, facts, 1)
		require.NotEqual(t, "RESUME_REQUESTED", facts[0].Control)
		return e
	}))
	require.Equal(t, "BUDGET_PAUSE", f.read(t, v.ID).StateReason)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "RESUMED"})
	require.ErrorIs(t, err, ErrConflict)
}

func TestExecutionHeldRunnerCanCancelFailOrBeLost(t *testing.T) {
	for _, kind := range []string{StateCanceled, "FAILED", "LOST"} {
		t.Run(kind, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, false); return e }))
			_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
			require.NoError(t, err)
			if kind == StateCanceled {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, f.read(t, v.ID).Version); return e }))
			}
			receipt, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: kind})
			require.NoError(t, err)
			expected := kind
			if kind == "LOST" {
				expected = "INTERRUPTED"
			}
			require.Equal(t, expected, receipt.State)
			var active int
			require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id=?", v.ID).Scan(&active).Error)
			require.Zero(t, active)
		})
	}
}

func TestExecutionControlReceiptsSurviveConcurrentCancellation(t *testing.T) {
	for _, kind := range []string{"PAUSED", "RESUMED"} {
		t.Run(kind, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, false); return e }))
			seq := int64(2)
			if kind == "RESUMED" {
				_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: seq, Kind: "PAUSED"})
				require.NoError(t, err)
				seq++
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, true); return e }))
			}
			directive, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: seq, Kind: "HEARTBEAT"})
			require.NoError(t, err)
			require.Equal(t, kind == "PAUSED", directive.PauseRequested)
			require.Equal(t, kind == "RESUMED", directive.ResumeRequested)
			seq++
			version := f.read(t, v.ID).Version
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, version); return e }))
			in := ReceiptInput{DispatchEpoch: 1, Seq: seq, Kind: kind}
			ack, err := f.receipt(v.ID, in)
			require.NoError(t, err)
			expected := "PAUSED"
			if kind == "RESUMED" {
				expected = "RUNNING"
			}
			require.Equal(t, expected, ack.State)
			require.True(t, ack.CancelRequested)
			require.False(t, ack.PauseRequested)
			require.False(t, ack.ResumeRequested)
			require.Equal(t, "USER_CANCEL", f.read(t, v.ID).StateReason)
			replay, err := f.receipt(v.ID, in)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.True(t, replay.CancelRequested)
			ack, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: seq + 1, Kind: StateCanceled})
			require.NoError(t, err)
			require.Equal(t, StateCanceled, ack.State)
		})
	}
}

func TestExecutionUnsolicitedResumeRetainsAcknowledgedHold(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, false); return e }))
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PAUSED"})
	require.NoError(t, err)
	ack, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "RESUMED"})
	require.NoError(t, err)
	require.Equal(t, "RUNNING", ack.State)
	require.True(t, ack.PauseRequested)
	require.Equal(t, "PAUSE_REQUESTED", f.read(t, v.ID).StateReason)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "PAUSED"})
	require.NoError(t, err)
	require.Equal(t, "HOLD_ACKNOWLEDGED", f.read(t, v.ID).StateReason)
}

func TestHoldPrincipalDiscoveryIsScopedSortedAndRejectsUnavailableSources(t *testing.T) {
	for _, fault := range []string{"none", "machine", "participants", "query", "invalid"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			before := f.read(t, v.ID)
			failure := errors.New("hold discovery unavailable")
			f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) {
				if fault == "participants" {
					return nil, failure
				}
				return []guid.ID{101, 21, 11, 21}, nil
			}
			if fault == "machine" {
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			}
			if fault == "query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("hold_discovery_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Executions.String() {
						tx.AddError(failure)
					}
				}))
			}
			var ids []guid.ID
			err := f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				work := v.WorkItemID
				if fault == "invalid" {
					work = -1
				}
				ids, e = f.m.holdPrincipalsOn(f.human, tx, 7, v.ProjectID, work)
				return e
			})
			if fault == "none" {
				require.NoError(t, err)
				require.Equal(t, []guid.ID{11, 21, 101}, ids)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					other, e := f.m.holdPrincipalsOn(f.human, tx, 7, v.ProjectID, v.WorkItemID+1)
					require.Empty(t, other)
					return e
				}))
			} else {
				require.Error(t, err)
				require.Nil(t, ids)
			}
			if fault == "query" {
				require.NoError(t, f.db.Callback().Query().Remove("hold_discovery_failure"))
			}
			require.Equal(t, before, f.read(t, v.ID))
		})
	}
}

func TestExecutionHoldBindingRejectsIncompleteConnector(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, module.Provide(s, delivery.ExecutionHoldConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, f.m.provideExecutionHolds(s), ErrUnavailable)
}
