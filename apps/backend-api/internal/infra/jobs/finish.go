package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeadLetter is the safe event payload for a job that exhausted its retries.
// It deliberately contains neither handler error text nor the job's payload.
type DeadLetter struct {
	JobID, TenantID   guid.ID
	Kind              string
	Attempts, Version int
	CorrelationID     string
}

var (
	errLeaseExpired    = errors.New("job lease expired")
	errInterrupted     = errors.New("job interrupted by shutdown")
	errConcurrencyBusy = errors.New("job concurrency key occupied")
)

type failureRecord struct {
	Attempt int       `json:"attempt"`
	At      time.Time `json:"at"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

func validClaim(row model.Job) bool {
	return row.ID > 0 && row.TenantID >= 0 && row.LeaseToken > 0 && row.LeaseOwner != nil && *row.LeaseOwner != ""
}

// heartbeat returns false when this worker no longer owns the running attempt.
// The execution loop must cancel its handler immediately on false or error.
func (m *Module) heartbeat(ctx context.Context, claim model.Job) (bool, error) {
	if !validClaim(claim) {
		return false, ErrInvalidRequest
	}
	updated := false
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		nowMS, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		table := query.BindJobs(tx, m.names)
		result, err := table.WithContext(ctx).Where(table.ID.Eq(claim.ID), table.TenantID.Eq(claim.TenantID), table.State.Eq("RUNNING"), table.LeaseOwner.Eq(*claim.LeaseOwner), table.LeaseToken.Eq(claim.LeaseToken)).UpdateSimple(table.LeaseExpiresAtMS.Value(nowMS+m.config.Lease.Milliseconds()), table.UpdatedAt.Value(time.UnixMilli(nowMS).UTC()), table.Version.Add(1))
		updated = result.RowsAffected == 1
		return err
	})
	if err != nil {
		return false, err
	}
	return updated, nil
}

// finish commits an owned attempt's outcome. Dead lettering requires the event
// append in the SAME transaction; a missing or failing appender leaves RUNNING
// untouched for recovery, rather than silently losing the mandatory event.
func (m *Module) finish(ctx context.Context, claim model.Job, handler Handler, failure error, onDead func(context.Context, *gorm.DB, DeadLetter) error) (bool, error) {
	if !validClaim(claim) {
		return false, ErrInvalidRequest
	}
	changed := false
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		// Retries increase PENDING and must serialize with new admissions. They are
		// retained even if newly admitted work has meanwhile filled that budget.
		if failure != nil {
			if err := m.guard(ctx, tx, "jobs:pending:"+claim.TenantID.String()); err != nil {
				return err
			}
		}
		table := query.BindJobs(tx, m.names)
		read := table.WithContext(ctx)
		if tx.Dialector.Name() != "sqlite" {
			read = read.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		current, err := read.Where(table.ID.Eq(claim.ID), table.TenantID.Eq(claim.TenantID), table.State.Eq("RUNNING"), table.LeaseOwner.Eq(*claim.LeaseOwner), table.LeaseToken.Eq(claim.LeaseToken)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		nowMS, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		if errors.Is(failure, errLeaseExpired) && (current.LeaseExpiresAtMS == nil || *current.LeaseExpiresAtMS >= nowMS) {
			return nil
		}
		now := time.UnixMilli(nowMS).UTC()
		state := "SUCCEEDED"
		updates := []field.AssignExpr{table.LeaseOwner.Null(), table.LeaseExpiresAtMS.Null(), table.UpdatedAt.Value(now), table.Version.Add(1)}
		if failure != nil {
			if !errors.Is(failure, errConcurrencyBusy) {
				var history []failureRecord
				if err := json.Unmarshal([]byte(current.ErrorHistory), &history); err != nil {
					return err
				}
				if history == nil {
					return ErrInvalidRequest
				}
				code, message := safeFailure(failure)
				history = append(history, failureRecord{Attempt: current.Attempts, At: now, Code: code, Message: message})
				if len(history) > 5 {
					history = history[len(history)-5:]
				}
				encoded, err := json.Marshal(history)
				if err != nil {
					return err
				}
				updates = append(updates, table.LastErrorCode.Value(code), table.LastError.Value(message), table.ErrorHistory.Value(string(encoded)))
			}

			delay, retry := retryDelay(handler, Job{Attempt: current.Attempts, MaxAttempts: current.MaxAttempts}, failure)
			if errors.Is(failure, errInterrupted) || errors.Is(failure, errConcurrencyBusy) {
				if current.Attempts < 1 {
					return ErrInvalidRequest
				}
				retry = true
				delay = 0
				if errors.Is(failure, errConcurrencyBusy) {
					delay = 5 * time.Second
				}
				updates = append(updates, table.Attempts.Sub(1))
			}
			state = "DEAD"
			if retry {
				state = "PENDING"
				delayMS := delay.Milliseconds()
				if delay%time.Millisecond != 0 {
					delayMS++
				}
				updates = append(updates, table.RunAtMS.Value(nowMS+delayMS))
			} else if onDead == nil {
				return ErrUnavailable
			}
		}
		if state == "PENDING" {
			updates = append(updates, table.FinishedAt.Null())
		} else {
			updates = append(updates, table.FinishedAt.Value(now))
		}
		updates = append(updates, table.State.Value(state))
		result, err := table.WithContext(ctx).Where(table.ID.Eq(current.ID), table.State.Eq("RUNNING"), table.LeaseOwner.Eq(*claim.LeaseOwner), table.LeaseToken.Eq(claim.LeaseToken)).UpdateSimple(updates...)
		if err != nil {
			return err
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if state == "DEAD" {
			event := DeadLetter{JobID: current.ID, TenantID: current.TenantID, Kind: current.Kind, Attempts: current.Attempts, Version: current.Version + 1, CorrelationID: current.CorrelationID}
			if err := onDead(ctx, tx, event); err != nil {
				return err
			}
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// Handler errors can contain payloads, credentials or panic details. Persist
// only bounded diagnostics; correlated operation logging owns troubleshooting.
func safeFailure(err error) (string, string) {
	if errors.Is(err, errHandlerPanic) {
		return "PANIC", "Handler panicked during execution."
	}
	if errors.Is(err, errLeaseExpired) {
		return "LEASE_EXPIRED", "Worker lease expired before completion."
	}
	if errors.Is(err, errInterrupted) {
		return "INTERRUPTED", "Execution interrupted by service shutdown."
	}
	var permanent *permanentError
	if errors.As(err, &permanent) {
		return "PERMANENT", "Handler reported a permanent failure."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT", "Handler exceeded its execution timeout."
	}
	return "HANDLER_ERROR", "Handler returned an error."
}
