package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationWatchdogFailureCannotSealOrReleasePartially(t *testing.T) {
	for _, fault := range []string{"missing_lock", "lock_error", "release_error", "write_error", "event_error", "read_error"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.pullVerificationOn(f.runner, tx, 7, f.fence); return e }))
			switch fault {
			case "missing_lock":
				f.m.machines.LockOn = nil
			case "lock_error":
				f.m.machines.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, ErrUnavailable
				}
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrUnavailable }
			case "write_error":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_expiry BEFORE UPDATE ON ex_execution_verification_tasks BEGIN SELECT RAISE(ABORT, 'expiry rejected'); END").Error)
			case "event_error":
				f.pub.fail = true
			case "read_error":
				require.NoError(t, f.db.Exec("DROP TABLE ex_execution_verification_tasks").Error)
			}
			require.Error(t, f.m.expireVerificationTasks(t.Context(), f.db, 7, time.Now().UTC().Add(2*time.Hour)))
			if fault == "read_error" {
				return
			}
			q := query.BindVerificationTasks(f.db, f.m.names)
			r, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			require.Equal(t, "LEASED", r.State)
			require.Empty(t, r.ReceiptHash)
			var active int
			require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id = ?", id).Scan(&active).Error)
			require.Equal(t, 1, active)
		})
	}
}

func TestVerificationPullFailuresCannotLeaseOrReserveCapacity(t *testing.T) {
	for _, fault := range []string{"session", "plan_corrupt", "missing_reserve", "reserve_error", "lease_update"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; id, err = f.m.queueVerificationOn(f.human, tx, in); return err }))
			switch fault {
			case "session":
				f.sessionValid = false
			case "plan_corrupt":
				q := query.BindVerificationTasks(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.PlanJSON, "{}")
				require.NoError(t, e)
			case "missing_reserve":
				f.m.machines.ReserveSlotOn = nil
			case "reserve_error":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, errors.New("capacity unavailable")
				}
			case "lease_update":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_verification_lease BEFORE UPDATE ON ex_execution_verification_tasks BEGIN SELECT RAISE(ABORT, 'lease rejected'); END").Error)
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.pullVerificationOn(f.runner, tx, 7, f.fence); return e })
			require.Error(t, err)
			q := query.BindVerificationTasks(f.db, f.m.names)
			task, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			require.Equal(t, "QUEUED", task.State)
			require.Zero(t, task.SessionID)
			var slots int64
			require.NoError(t, f.db.Table("slots").Where("execution_id = ?", id).Count(&slots).Error)
			require.Zero(t, slots)
		})
	}
}

func TestVerificationCompletionFailuresRollbackReceiptAndCapacityRelease(t *testing.T) {
	for _, fault := range []string{"missing_task", "plan_corrupt", "expired", "missing_started", "future_ended", "missing_release", "release_error", "write_error", "audit_error"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; id, err = f.m.queueVerificationOn(f.human, tx, in); return err }))
			var plans []VerificationPlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
				return e
			}))
			require.Len(t, plans, 1)
			b, _ := json.Marshal(plans[0])
			code := 0
			o := VerificationObservation{PlanHash: hash(b), HeadCommit: in.HeadCommit, ExitCode: &code, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
			target := id
			q := query.BindVerificationTasks(f.db, f.m.names)
			switch fault {
			case "missing_task":
				target = id + 1
			case "plan_corrupt":
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.PlanJSON, "{}")
				require.NoError(t, e)
			case "expired":
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.LeaseExpiresAt, time.Now().Add(-time.Minute))
				require.NoError(t, e)
			case "missing_started":
				o.StartedAt = time.Time{}
			case "future_ended":
				o.EndedAt = time.Now().UTC().Add(time.Hour)
			case "missing_release":
				f.m.machines.ReleaseSlotOn = nil
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
					return errors.New("release unavailable")
				}
			case "write_error":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_verification_receipt BEFORE UPDATE ON ex_execution_verification_tasks BEGIN SELECT RAISE(ABORT, 'write rejected'); END").Error)
			case "audit_error":
				f.pub.fail = true
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.completeVerificationOn(f.runner, tx, 7, target, f.fence, plans[0].Nonce, o)
			})
			require.Error(t, err)
			task, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			require.Equal(t, "LEASED", task.State)
			require.Empty(t, task.ReceiptHash)
			var active int
			require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id = ?", id).Scan(&active).Error)
			require.Equal(t, 1, active)
		})
	}
}

func TestVerificationReceiptReaderRejectsCorruptOrUnboundImmutableFacts(t *testing.T) {
	for _, fault := range []string{"plan_hash", "observation_hash", "plan_json", "observation_json", "observation_plan"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
			q := query.BindVerificationTasks(f.db, f.m.names)
			r, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			o := VerificationObservation{PlanHash: r.PlanHash, Reason: "MACHINE_UNAVAILABLE", StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
			b, _ := json.Marshal(o)
			updates := map[string]any{"state": "DONE", "observation_json": string(b), "receipt_hash": hash(b)}
			switch fault {
			case "plan_hash":
				updates["plan_hash"] = "wrong"
			case "observation_hash":
				updates["receipt_hash"] = "wrong"
			case "plan_json":
				updates["plan_json"] = "invalid"
				updates["plan_hash"] = hash([]byte("invalid"))
			case "observation_json":
				updates["observation_json"] = "invalid"
				updates["receipt_hash"] = hash([]byte("invalid"))
			case "observation_plan":
				o.PlanHash = "other"
				b, _ = json.Marshal(o)
				updates["observation_json"] = string(b)
				updates["receipt_hash"] = hash(b)
			}
			_, e = q.WithContext(t.Context()).Where(q.ID.Eq(id)).Updates(updates)
			require.NoError(t, e)
			e = f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, 50, id); return err })
			require.ErrorIs(t, e, ErrUnavailable)
		})
	}
}
