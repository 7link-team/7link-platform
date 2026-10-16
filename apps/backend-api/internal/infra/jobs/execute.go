package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/jobs/model"
	"gorm.io/gorm"
)

var errHandlerPanic = errors.New("job handler panicked")

// invokeHandler isolates a handler panic from the execution loop. Panic values
// can contain payloads or credentials, so only the stack and job identity are
// logged; persistence receives a safe classification. The caller owns the
// persisted timeout, heartbeat, worker slot and shutdown lifecycle.
func invokeHandler(ctx context.Context, handler Handler, job Job) (err error) {
	defer func() {
		if recover() != nil {
			slog.ErrorContext(ctx, "background job handler panicked", "job_id", job.ID, "kind", job.Kind, "stack", string(debug.Stack()))
			err = errHandlerPanic
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = handler.Run(ctx, job)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

var errExecutionLeaseLost = errors.New("job execution lease lost")

// concurrencyLockName preserves the documented name when it fits dlock's
// bound. Long tuples use a separate namespace and a length-delimited digest;
// truncating kind or key would merge unrelated execution locks.
func concurrencyLockName(kind, key string) string {
	name := "jobs:ck:" + kind + ":" + key
	if len(name) <= 191 {
		return name
	}
	return fmt.Sprintf("jobs:ck-sha256:%x", sha256.Sum256([]byte(strconv.Itoa(len(kind))+":"+kind+key)))
}

// executeClaim occupies its caller's worker slot until the handler returns.
// Lifecycle owns the outer shutdown deadline. A timed-out handler receives a
// 30-second grace period, then is settled but retains its slot until return.
// A lost lease leaves the row for recovery and discards the handler result.
func (m *Module) executeClaim(ctx context.Context, claim model.Job, onDead func(context.Context, *gorm.DB, DeadLetter) error) (bool, error) {
	if !validClaim(claim) || claim.TimeoutMS <= 0 || claim.TimeoutMS > int64((1<<63-1)/time.Millisecond) {
		return false, ErrInvalidRequest
	}
	if m.recorder == nil || m.tryAcquire == nil || m.config.Lease < time.Millisecond {
		return false, ErrUnavailable
	}
	m.mu.RLock()
	handler, ok := m.handlers[claim.Kind]
	m.mu.RUnlock()
	if !ok {
		return false, ErrUnknownKind
	}
	// Keep renewal alive until the handler actually returns, including the
	// timeout grace period; a deadline is not evidence that work has stopped.
	leaseCtx, cancelLease := context.WithCancelCause(ctx)
	defer cancelLease(context.Canceled)
	finish := func(failure error) (bool, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return m.finish(cleanupCtx, claim, handler, failure, onDead)
	}
	if ctx.Err() != nil {
		return finish(errInterrupted)
	}
	if claim.ConcurrencyKey != nil && *claim.ConcurrencyKey != "" {
		lock, err := m.tryAcquire(leaseCtx, concurrencyLockName(claim.Kind, *claim.ConcurrencyKey), m.config.Lease, func(string, error) { cancelLease(errExecutionLeaseLost) })
		if errors.Is(err, dlock.ErrNotAcquired) {
			return finish(errConcurrencyBusy)
		}
		if err != nil {
			return false, err
		}
		if lock == nil {
			return false, ErrUnavailable
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := lock.Unlock(releaseCtx); err != nil {
				slog.ErrorContext(releaseCtx, "release job concurrency lease", "job_id", claim.ID, "error", err)
			}
		}()
	}
	// Check ownership before invoking even a fast handler on an old claim.
	owned, err := m.heartbeat(leaseCtx, claim)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, errExecutionLeaseLost
	}
	monitorCtx, stopMonitor := context.WithCancel(leaseCtx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(m.config.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				owned, err := m.heartbeat(monitorCtx, claim)
				if err != nil || !owned {
					if monitorCtx.Err() == nil {
						cancelLease(errExecutionLeaseLost)
					}
					return
				}
			}
		}
	}()
	runCtx, cancelRun := context.WithTimeout(leaseCtx, time.Duration(claim.TimeoutMS)*time.Millisecond)
	defer cancelRun()
	job := Job{ID: claim.ID, TenantID: claim.TenantID, Kind: claim.Kind, Queue: claim.Queue, Payload: []byte(claim.Payload), Attempt: claim.Attempts, MaxAttempts: claim.MaxAttempts, CorrelationID: claim.CorrelationID}
	if claim.CreatedBy != nil {
		job.CreatedBy = *claim.CreatedBy
	}
	result := make(chan error, 1)
	go func() {
		var failure error
		_ = audit.Run(runCtx, m.recorder, audit.Spec{
			Entry: audit.KindJob, Operation: claim.Kind, Action: claim.Kind,
			ResourceType: "background_job", ResourceID: claim.ID.String(),
			Actor:    audit.Actor{Type: audit.ActorSystem, TenantID: claim.TenantID.String()},
			Metadata: map[string]string{"attempt": strconv.Itoa(claim.Attempts), "correlation_id": claim.CorrelationID},
		}, func(ctx context.Context) error {
			failure = invokeHandler(ctx, handler, job)
			if failure == nil {
				return nil
			}
			// Preserve retry wrappers for finish, but never persist handler secrets in
			// the operation log's generic error field.
			_, message := safeFailure(failure)
			return errors.New(message)
		})
		result <- failure
	}()
	var failure error
	select {
	case failure = <-result:
	case <-runCtx.Done():
		grace := time.NewTimer(30 * time.Second)
		defer grace.Stop()
		select {
		case failure = <-result:
		case <-leaseCtx.Done():
			stopMonitor()
			<-monitorDone
			changed, err := false, errExecutionLeaseLost
			if !errors.Is(context.Cause(leaseCtx), errExecutionLeaseLost) {
				changed, err = finish(errInterrupted)
			}
			<-result // Keep this worker slot occupied until the real handler exits.
			return changed, err
		case <-grace.C:
			stopMonitor()
			<-monitorDone
			m.leakedWorkers.WithLabelValues(claim.Queue).Inc()
			defer m.leakedWorkers.WithLabelValues(claim.Queue).Dec()
			changed, err := false, errExecutionLeaseLost
			if !errors.Is(context.Cause(leaseCtx), errExecutionLeaseLost) {
				failure := context.DeadlineExceeded
				if ctx.Err() != nil {
					failure = errInterrupted
				}
				changed, err = finish(failure)
			}
			<-result // Timing out cannot make an ignored-cancellation handler disappear.
			return changed, err
		}
	}
	stopMonitor()
	<-monitorDone
	if errors.Is(context.Cause(leaseCtx), errExecutionLeaseLost) {
		return false, errExecutionLeaseLost
	}
	if ctx.Err() != nil {
		failure = errInterrupted
	}
	return finish(failure)
}
