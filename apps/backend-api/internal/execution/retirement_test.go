package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementExecutionReconciliationWaitsThenFencesUnknownWithAtomicSourceDetach(t *testing.T) {
	for _, scenario := range []string{"unleased", "delivered", "watchdog", "detach_failure", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := executionHarness(t)
			created := f.create(t)
			q := query.BindExecutions(f.db, f.m.names)
			row, err := q.WithContext(f.human).Where(q.ID.Eq(created.ID)).First()
			require.NoError(t, err)
			var plan ExecutionPlan
			require.NoError(t, json.Unmarshal([]byte(row.PlanJSON), &plan))
			plan.ExecutorPrincipalID = 21
			raw, err := json.Marshal(plan)
			require.NoError(t, err)
			_, err = q.WithContext(f.human).Where(q.ID.Eq(created.ID)).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)})
			require.NoError(t, err)
			f.m.handover.authority.AuthorizeArtifactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			if scenario != "unleased" {
				f.pull(t, created.ID)
				_, err = f.receipt(created.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, err)
			}
			detaches := 0
			failure := errors.New("source detach failed")
			detach := func(_ context.Context, _ *gorm.DB, l delivery.ExecutionWorkLink) error {
				detaches++
				require.Equal(t, created.ID, l.ExecutionID)
				if scenario == "detach_failure" {
					return failure
				}
				return nil
			}
			reconcile := func() (bool, error) {
				var ready bool
				err := f.db.Transaction(func(tx *gorm.DB) error {
					var err error
					ready, err = f.m.reconcileRetirementExecutionsOn(f.human, tx, 7, 21, 31, detach)
					return err
				})
				return ready, err
			}
			if scenario == "revoked" {
				f.authorized = false
				_, err = reconcile()
				require.Error(t, err)
				f.authorized = true
				require.Equal(t, "RUNNING", f.read(t, created.ID).State)
				return
			}
			ready, err := reconcile()
			require.NoError(t, err)
			if scenario == "unleased" {
				require.True(t, ready)
				require.Equal(t, "CONFIRMED", f.read(t, created.ID).ResultState)
				require.Zero(t, detaches)
				return
			}
			require.False(t, ready)
			require.Zero(t, detaches)
			past := time.Now().UTC().Add(-time.Minute)
			_, err = q.WithContext(f.human).Where(q.ID.Eq(created.ID)).Update(q.CancelRequestedAt, past)
			require.NoError(t, err)
			if scenario == "watchdog" {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ExpireOn(f.human, tx, 7, created.ID, time.Now().UTC()) }))
			}
			before := f.read(t, created.ID)
			ready, err = reconcile()
			if scenario == "detach_failure" {
				require.ErrorIs(t, err, failure)
				require.Equal(t, before.State, f.read(t, created.ID).State)
				require.Equal(t, before.DispatchEpoch, f.read(t, created.ID).DispatchEpoch)
				return
			}
			require.NoError(t, err)
			require.True(t, ready)
			require.Equal(t, 1, detaches)
			v := f.read(t, created.ID)
			require.Equal(t, StateCanceled, v.State)
			require.Equal(t, "UNKNOWN", v.ResultState)
			require.EqualValues(t, 2, v.DispatchEpoch)
			_, err = f.receipt(created.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "CANCELLED"}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			require.Error(t, err)
			ready, err = reconcile()
			require.NoError(t, err)
			require.True(t, ready)
			require.Equal(t, 1, detaches)
		})
	}
}

func TestRetirementReconciliationStorageFailuresPreserveUnknownRuntimeAndSource(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f := executionHarness(t)
		v := f.create(t)
		q := query.BindExecutions(f.db, f.m.names)
		row, e := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).First()
		require.NoError(t, e)
		var plan ExecutionPlan
		require.NoError(t, json.Unmarshal([]byte(row.PlanJSON), &plan))
		plan.ExecutorPrincipalID = 21
		raw, e := json.Marshal(plan)
		require.NoError(t, e)
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
		f.pull(t, v.ID)
		_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
		require.NoError(t, e)
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("cancel_requested_at", time.Now().UTC().Add(-time.Minute)).Error)
		f.m.handover.authority.AuthorizeArtifactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
		return f, func(tx *gorm.DB) error {
			ready, e := f.m.reconcileRetirementExecutionsOn(f.human, tx, 7, 21, 31, func(_ context.Context, tx *gorm.DB, _ delivery.ExecutionWorkLink) error {
				return tx.Exec("INSERT INTO effects(kind) VALUES ('source.detached')").Error
			})
			if e == nil {
				require.True(t, ready)
			}
			return e
		}
	})
}
