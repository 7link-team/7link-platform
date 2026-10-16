package jobs

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"7link.com/platform/internal/infra/jobs/model"
	"gorm.io/gorm"
)

// DeadLetterConnector lets the composition root join job settlement to the
// event publisher without a reverse jobs -> events lifecycle dependency.
// Bind once during wiring, before Initialize seals the module.
type DeadLetterConnector struct {
	Bind func(func(context.Context, *gorm.DB, DeadLetter) error) error
}

func (m *Module) bindDeadLetter(appendEvent func(context.Context, *gorm.DB, DeadLetter) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if appendEvent == nil || m.onDead != nil {
		return ErrInvalidRequest
	}
	m.onDead = appendEvent
	return nil
}

type workerRun struct {
	stopClaims, cancelExecutions context.CancelFunc
	claimsDone, done             chan struct{}
	claims, executions           sync.WaitGroup
	mu                           sync.Mutex
	active                       map[int64]model.Job
	interrupted                  bool
}

// Start launches the enabled scheduler and one claimer per worker queue.
// The explicit lifecycle owns
// cancellation, so canceling a startup request does not bypass graceful drain.
func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sealed {
		return ErrUnavailable
	}
	if m.workers != nil {
		return nil
	}
	workersEnabled := m.config.WorkersEnabled == nil || *m.config.WorkersEnabled
	schedulerEnabled := m.config.SchedulerEnabled == nil || *m.config.SchedulerEnabled
	if !workersEnabled && !schedulerEnabled {
		return nil
	}
	if workersEnabled && m.onDead == nil {
		return ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	claimCtx, stopClaims := context.WithCancel(context.WithoutCancel(ctx))
	runCtx, cancelExecutions := context.WithCancel(context.WithoutCancel(ctx))
	run := &workerRun{stopClaims: stopClaims, cancelExecutions: cancelExecutions, claimsDone: make(chan struct{}), done: make(chan struct{}), active: map[int64]model.Job{}}
	m.workers = run
	if workersEnabled {
		for queue, slots := range m.config.Queues {
			run.claims.Add(1)
			go func() {
				defer run.claims.Done()
				m.workQueue(claimCtx, runCtx, run, queue, strconv.FormatInt(id, 10), slots)
			}()
		}
		run.claims.Add(1)
		go func() {
			defer run.claims.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				if _, err := m.reapExpired(claimCtx, m.onDead); err != nil && claimCtx.Err() == nil {
					slog.ErrorContext(claimCtx, "recover expired background jobs", "error", err)
				}
				select {
				case <-claimCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	if schedulerEnabled {
		run.claims.Add(1)
		go func() { defer run.claims.Done(); m.runScheduler(claimCtx) }()
	}
	go func() {
		run.claims.Wait()
		close(run.claimsDone)
		// No execution Add is possible once every claimer has exited.
		run.executions.Wait()
		close(run.done)
	}()
	return nil
}

func (m *Module) workQueue(claimCtx, runCtx context.Context, run *workerRun, queue, owner string, capacity int) {
	available := capacity
	completed := make(chan struct{}, capacity)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-claimCtx.Done():
			return
		case <-completed:
			available++
		case <-timer.C:
		}
		if claimCtx.Err() != nil {
			return
		}
		slots := min(available, m.config.ClaimBatch)
		if slots > 0 {
			rows, err := m.claim(claimCtx, queue, owner, slots)
			if err != nil && claimCtx.Err() == nil {
				slog.ErrorContext(claimCtx, "claim background jobs", "queue", queue, "error", err)
			}
			for _, row := range rows {
				available--
				run.mu.Lock()
				run.active[int64(row.ID)] = row
				run.mu.Unlock()
				run.executions.Add(1)
				go func() {
					defer run.executions.Done()
					if _, err := m.executeClaim(runCtx, row, m.onDead); err != nil && !errors.Is(err, errExecutionLeaseLost) {
						slog.ErrorContext(runCtx, "execute background job", "job_id", row.ID, "error", err)
					}
					run.mu.Lock()
					delete(run.active, int64(row.ID))
					run.mu.Unlock()
					completed <- struct{}{}
				}()
			}
		}
		timer.Reset(m.config.PollInterval)
	}
}

// Quiesce stops new claims and drains up to the configured budget. On expiry,
// interrupt owned rows through the same fenced finish path. Stuck handlers are
// canceled but never treated as available slots or waited on without a bound.
func (m *Module) Quiesce(ctx context.Context) error {
	m.mu.RLock()
	run := m.workers
	m.mu.RUnlock()
	if run == nil {
		return nil
	}
	run.stopClaims()
	run.mu.Lock()
	budget := m.config.DrainTimeout
	if run.interrupted {
		budget = 0
	}
	run.mu.Unlock()
	drainCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	select {
	case <-run.done:
		run.cancelExecutions()
		return nil
	case <-drainCtx.Done():
		run.cancelExecutions()
		run.mu.Lock()
		run.interrupted = true
		run.mu.Unlock()
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cleanupCancel()
	// Claim cancellation must settle before snapshotting; otherwise a newly
	// committed claim could escape the interruption pass.
	select {
	case <-run.claimsDone:
	case <-cleanupCtx.Done():
		return cleanupCtx.Err()
	}
	run.mu.Lock()
	pending := make([]model.Job, 0, len(run.active))
	for _, row := range run.active {
		pending = append(pending, row)
	}
	run.mu.Unlock()
	var failures error
	for _, row := range pending {
		m.mu.RLock()
		handler := m.handlers[row.Kind]
		m.mu.RUnlock()
		_, err := m.finish(cleanupCtx, row, handler, errInterrupted, m.onDead)
		failures = errors.Join(failures, err)
	}
	return failures
}

// Stop also handles startup rollback paths that did not call Quiesce.
func (m *Module) Stop(ctx context.Context) error { return m.Quiesce(ctx) }
