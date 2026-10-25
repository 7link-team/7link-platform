package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestControlFailuresRollbackSlotAndExecutionTogether(t *testing.T) {
	for _, operation := range []string{"cancel", "recover", "expire"} {
		for _, fault := range []string{"machine_missing", "machine_error", "machine_lock_missing", "machine_lock_error", "principal_error", "authority", "work_missing", "work_error", "slot_missing", "slot_error", "update", "outbox", "version"} {
			if operation == "expire" && (fault == "authority" || fault == "work_missing" || fault == "work_error" || fault == "version") {
				continue // The watchdog does not perform Human/domain control or version checks.
			}
			t.Run(operation+"/"+fault, func(t *testing.T) {
				f := executionHarness(t)
				v := f.create(t)
				if operation == "recover" {
					f.pull(t, v.ID)
					_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOST"})
					require.NoError(t, err)
				}
				before := f.read(t, v.ID)
				var effects int64
				require.NoError(t, f.db.Table("effects").Count(&effects).Error)
				var activeBefore int
				require.NoError(t, f.db.Raw("SELECT active FROM slots WHERE execution_id=?", v.ID).Scan(&activeBefore).Error)
				version := before.Version
				if operation == "expire" {
					f.machineState = "OFFLINE"
				}
				switch fault {
				case "machine_missing":
					f.m.machines.ByIDOn = nil
				case "machine_error":
					f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
						return fleet.MachineFact{}, ErrUnavailable
					}
				case "machine_lock_missing":
					f.m.machines.LockOn = nil
				case "machine_lock_error":
					f.m.machines.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
						return fleet.MachineFact{}, ErrUnavailable
					}
				case "principal_error":
					f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrForbidden }
				case "authority":
					f.authorized = false
				case "work_missing":
					if operation == "cancel" {
						f.m.work.FinishedOn = nil
					} else {
						f.m.work.AuthorizeOn = nil
					}
				case "work_error":
					if operation == "cancel" {
						f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return ErrCapabilityUnavailable }
					} else {
						f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrForbidden }
					}
				case "slot_missing":
					if operation == "recover" {
						f.m.machines.ReacquireSlotOn = nil
					} else {
						f.m.machines.ReleaseSlotOn = nil
					}
				case "slot_error":
					if operation == "recover" {
						f.m.machines.ReacquireSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
							return fleet.SlotFact{}, ErrUnavailable
						}
					} else {
						f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrUnavailable }
					}
				case "update":
					require.NoError(t, f.db.Exec(fmt.Sprintf("CREATE TRIGGER reject_control BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT, 'test control write failure'); END", f.m.names.Executions.String())).Error)
				case "outbox":
					f.pub.fail = true
				case "version":
					version++
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch operation {
					case "cancel":
						_, err := f.m.CancelOn(f.human, tx, 7, v.ID, version)
						return err
					case "recover":
						_, err := f.m.RecoverOn(f.human, tx, 7, v.ID, version, true)
						return err
					default:
						return f.m.ExpireOn(t.Context(), tx, 7, v.ID, time.Now().UTC())
					}
				})
				require.Error(t, err)
				require.Equal(t, before, f.read(t, v.ID))
				var active int
				require.NoError(t, f.db.Raw("SELECT active FROM slots WHERE execution_id=?", v.ID).Scan(&active).Error)
				require.Equal(t, activeBefore, active)
				var count int64
				require.NoError(t, f.db.Table("effects").Count(&count).Error)
				require.Equal(t, effects, count)
			})
		}
	}
}

func TestCancelVisibleToRunnerAndTimeoutUnknown(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	v = f.read(t, v.ID)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CancelOn(f.human, tx, 7, v.ID, v.Version); return e }))
	require.Equal(t, "RUNNING", v.State)
	require.NotNil(t, v.CancelRequestedAt)
	cancel := f.pull(t, v.ID)
	require.NotNil(t, cancel.CancelRequestedAt)
	r, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "HEARTBEAT"})
	require.NoError(t, err)
	require.True(t, r.CancelRequested)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.ExpireOn(t.Context(), tx, 7, v.ID, v.CancelRequestedAt.Add(31*time.Second))
	}))
	v = f.read(t, v.ID)
	require.Equal(t, "INTERRUPTED", v.State)
	require.Equal(t, "CANCEL_TIMEOUT", v.StateReason)
	require.Equal(t, "UNKNOWN", v.ResultState)
}
func TestRecoverPreservesPlanAndFencesOldEpoch(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOST"})
	require.NoError(t, err)
	lost := f.read(t, v.ID)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecoverOn(f.human, tx, 7, v.ID, lost.Version, false); return e }), ErrInvalidArgument)
	var recovered ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		recovered, e = f.m.RecoverOn(f.human, tx, 7, v.ID, lost.Version, true)
		return e
	}))
	require.Equal(t, v.Plan, recovered.Plan)
	require.Equal(t, v.PlanHash, recovered.PlanHash)
	require.Equal(t, int64(2), recovered.DispatchEpoch)
	require.Zero(t, recovered.LastSeq)
	require.Nil(t, recovered.LeaseExpiresAt)
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOST"})
	require.ErrorIs(t, err, ErrFenced)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 2, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
}
func TestUnleasedCancellationAndOfflineNeverFabricateSuccess(t *testing.T) {
	for _, state := range []string{"OFFLINE", "REVOKED", "ONLINE"} {
		t.Run(state, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			if state == "ONLINE" {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, v.Version); return e }))
				require.Equal(t, StateCanceled, f.read(t, v.ID).State)
				return
			}
			f.machineState = state
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ExpireOn(t.Context(), tx, 7, v.ID, time.Now().UTC()) }))
			out := f.read(t, v.ID)
			require.Equal(t, "INTERRUPTED", out.State)
			require.Equal(t, "UNKNOWN", out.ResultState)
			require.Equal(t, "MACHINE_"+state, out.StateReason)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecoverOn(f.human, tx, 7, v.ID, out.Version, true); return e }))
		})
	}
}

func TestRecoveredCancellationRetainsUnknownPriorSpend(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(fmt.Sprint(dispatched), func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOST"})
			require.NoError(t, err)
			v = f.read(t, v.ID)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = f.m.RecoverOn(f.human, tx, 7, v.ID, v.Version, true)
				return e
			}))
			var finished *WorkResult
			f.m.work.FinishedOn = func(_ context.Context, _ *gorm.DB, _ WorkLink, r WorkResult) error { finished = &r; return nil }
			if dispatched {
				f.pull(t, v.ID)
				v = f.read(t, v.ID)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, v.Version); return e }))
				_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 2, Seq: 1, Kind: StateCanceled, Usage: &ExecutionUsage{InputTokens: 100, OutputTokens: 20}})
				require.NoError(t, err)
			} else {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, v.Version); return e }))
			}
			require.NotNil(t, finished)
			require.False(t, finished.Unused)
			require.Nil(t, finished.ActualMicros)
		})
	}
}

func TestControlEveryStorageFailurePreservesUnknownOutcomeAndLease(t *testing.T) {
	for _, command := range []string{"cancel", "expire", "recover"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := executionHarness(t)
				v := f.create(t)
				if command != "cancel" {
					f.pull(t, v.ID)
				}
				if command == "recover" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ExpireOn(f.human, tx, 7, v.ID, time.Now().Add(time.Hour)) }))
					v = f.read(t, v.ID)
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "cancel":
						_, e := f.m.CancelOn(f.human, tx, 7, v.ID, v.Version)
						return e
					case "recover":
						_, e := f.m.RecoverOn(f.human, tx, 7, v.ID, v.Version, true)
						return e
					default:
						return f.m.ExpireOn(f.human, tx, 7, v.ID, time.Now().Add(time.Hour))
					}
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}
