package jobs

import (
	"context"
	"log/slog"
	"time"
)

// Singleton runs a cooperative loop under a renewable database lease. Callers
// must stop on cancellation and fence durable effects with transactional CAS or
// idempotency: cancellation cannot revoke an already-issued external effect.
type Singleton interface {
	Run(ctx context.Context, lockName string, loop func(context.Context) error)
}

// Run blocks until cancellation or successful loop completion. Lease loss and
// loop failure release ownership before retrying. A loop ignoring cancellation
// is never replaced locally while it is still running.
func (m *Module) Run(ctx context.Context, lockName string, loop func(context.Context) error) {
	if m == nil || m.acquire == nil || lockName == "" || loop == nil {
		slog.ErrorContext(ctx, "cannot run singleton", "lock", lockName, "error", ErrUnavailable)
		return
	}
	for ctx.Err() == nil {
		runCtx, cancel := context.WithCancelCause(ctx)
		lease, err := m.acquire(runCtx, lockName, 30*time.Second, func(_ string, lost error) { cancel(lost) })
		if err == nil && lease == nil {
			err = ErrUnavailable
		}
		if err == nil {
			func() {
				// Defer release even if the loop panics. Keep panic semantics intact.
				defer func() {
					cancel(context.Canceled)
					releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer releaseCancel()
					if releaseErr := lease.Unlock(releaseCtx); releaseErr != nil {
						slog.ErrorContext(ctx, "release singleton lease", "lock", lockName, "error", releaseErr)
					}
				}()
				if runCtx.Err() != nil {
					err = context.Cause(runCtx)
					return
				}
				err = loop(runCtx)
				if err == nil && runCtx.Err() != nil {
					err = context.Cause(runCtx)
				}
			}()
		} else {
			cancel(err)
		}
		if err == nil || ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "singleton loop will retry", "lock", lockName, "error", err)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
