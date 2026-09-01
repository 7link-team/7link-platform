// Package wuid allocates a process-unique 16-bit worker ID using either the
// WUID environment variable or a renewable database lease.
package wuid

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid/model"
	"7link.com/platform/internal/infra/wuid/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

const (
	MaxWorkerID        = 1<<16 - 1
	DefaultLease       = time.Hour
	renewRetryInterval = 5 * time.Second
	EnvKey             = "WUID"
	allocLockName      = "wuid:alloc"

	// allocLockTTL bounds how long a process that dies mid-allocation blocks
	// every other process from allocating a worker ID. It is deliberately
	// independent of the worker lease: allocation is a few statements, so
	// leasing the allocation lock for the worker lease would stall the whole
	// fleet's startup for an hour after a single crash.
	allocLockTTL = 15 * time.Second
)

var (
	ErrDatabaseUnavailable = errors.New("wuid: writable database unavailable")
	errLeaseTakenOver      = errors.New("wuid: lease taken over")
	processStarted         = time.Now()
)

type config struct {
	lease       time.Duration
	tablePrefix string
	onLost      func(error)
	onReacquire func(uint16)
}

type Option func(*config)

func WithLease(lease time.Duration) Option {
	return func(config *config) {
		if lease > 0 {
			config.lease = lease
		}
	}
}

// WithTablePrefix points this worker at a prefixed worker_ids table. It must
// match the prefix the migrations ran with, so it comes from the shared runtime
// rather than being set per call site.
func WithTablePrefix(prefix string) Option {
	return func(config *config) { config.tablePrefix = prefix }
}

func OnLost(callback func(error)) Option {
	return func(config *config) {
		previous := config.onLost
		config.onLost = func(err error) {
			if previous != nil {
				previous(err)
			}
			if callback != nil {
				callback(err)
			}
		}
	}
}

func OnReacquire(callback func(uint16)) Option {
	return func(config *config) {
		previous := config.onReacquire
		config.onReacquire = func(id uint16) {
			if previous != nil {
				previous(id)
			}
			if callback != nil {
				callback(id)
			}
		}
	}
}

func newConfig(options ...Option) config {
	config := config{lease: DefaultLease}
	for _, option := range options {
		option(&config)
	}
	return config
}

type Worker struct {
	db          *gorm.DB
	nowExpr     string
	lease       time.Duration
	tablePrefix string
	static      bool

	id    atomic.Int64
	token atomic.Value

	cancel context.CancelFunc
	wg     sync.WaitGroup

	alive      atomic.Bool
	generation atomic.Uint64
	lastRenew  atomic.Int64

	mu          sync.Mutex
	onLost      func(error)
	onReacquire func(uint16)
	closed      bool
}

func (worker *Worker) ID() uint16 {
	return uint16(worker.id.Load())
}

func (worker *Worker) Alive() bool { return worker.alive.Load() }

// leaseToken reads the current lease token. Every leased worker stores one
// before its heartbeat starts, and a static worker never reaches the lease
// statements; the comma-ok form keeps an unset token a no-match (and so a
// clean errLeaseTakenOver) instead of a panic.
// workers is the only place the worker_ids table is named for this worker, so
// a prefixed deployment cannot end up half-applied.
func (worker *Worker) workers() *query.WorkerRows {
	return query.Bind(worker.db, worker.tablePrefix)
}

func (worker *Worker) leaseToken() string {
	token, _ := worker.token.Load().(string)
	return token
}

// Generation changes whenever a leased worker reacquires a lease, including
// when the numeric worker ID happens to be reused.
func (worker *Worker) Generation() uint64 { return worker.generation.Load() }

func newStatic(id uint16) *Worker {
	worker := &Worker{static: true}
	worker.id.Store(int64(id))
	worker.generation.Store(1)
	worker.alive.Store(true)
	return worker
}

func Open(ctx context.Context, db *gorm.DB, options ...Option) (*Worker, error) {
	if id, ok, err := fromEnv(EnvKey); err != nil {
		return nil, err
	} else if ok {
		return newStatic(id), nil
	}
	return allocate(ctx, db, options...)
}

func allocate(ctx context.Context, db *gorm.DB, options ...Option) (*Worker, error) {
	if db == nil {
		return nil, ErrDatabaseUnavailable
	}
	config := newConfig(options...)
	nowExpr := gormx.NowMillisExpr(db.Dialector.Name())
	id, token, err := claimUnderLock(ctx, db, config.lease, nowExpr, config.tablePrefix)
	if err != nil {
		return nil, err
	}

	worker := &Worker{
		db: db, nowExpr: nowExpr, lease: config.lease,
		tablePrefix: config.tablePrefix,
		onLost:      config.onLost, onReacquire: config.onReacquire,
	}
	worker.id.Store(int64(id))
	worker.generation.Store(1)
	worker.token.Store(token)
	worker.alive.Store(true)
	worker.lastRenew.Store(monotonicMillis())

	heartbeatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	worker.cancel = cancel
	worker.wg.Add(1)
	go worker.run(heartbeatCtx)
	return worker, nil
}

// allocLocker builds the locker that serializes worker-id allocation. Auto-
// refresh keeps a slow allocation alive past the short TTL, so the lease can be
// bounded for crash recovery without risking expiry while it is legitimately
// held.
func allocLocker(db *gorm.DB, prefix string) *dlock.Locker {
	return dlock.NewLocker(db, dlock.WithTTL(allocLockTTL), dlock.WithAutoRefresh(), dlock.WithTablePrefix(prefix))
}

func claimUnderLock(ctx context.Context, db *gorm.DB, lease time.Duration, nowExpr, prefix string) (int, string, error) {
	token, err := newToken()
	if err != nil {
		return 0, "", fmt.Errorf("wuid: create lease token: %w", err)
	}
	lock, err := allocLocker(db, prefix).Lock(ctx, allocLockName)
	if err != nil {
		return 0, "", fmt.Errorf("wuid: acquire allocation lock: %w", err)
	}
	id, claimErr := claimID(ctx, db, token, collectSysinfo(), lease.Milliseconds(), nowExpr, prefix)
	unlockErr := lock.Unlock(context.WithoutCancel(ctx))
	if claimErr != nil {
		return 0, "", errors.Join(claimErr, unlockErr)
	}
	if unlockErr != nil {
		return 0, "", fmt.Errorf("wuid: release allocation lock: %w", unlockErr)
	}
	return id, token, nil
}

func claimID(ctx context.Context, db *gorm.DB, token string, info sysinfo, leaseMS int64, nowExpr, prefix string) (int, error) {
	workers := query.Bind(db, prefix)
	// expired and deadline hold the only raw SQL left: the database clock is a
	// function call, not a bindable value. The columns they compare and assign
	// are the generated, compile-checked ones.
	expiredNow := workers.ExpiresAt.LtCol(field.NewUnsafeFieldRaw(nowExpr))
	deadline := field.NewUnsafeFieldRaw(nowExpr+" + ?", leaseMS)

	reclaimable, err := workers.WithContext(ctx).
		Where(expiredNow).
		Order(workers.ID).
		Limit(1).
		Find()
	if err != nil {
		return 0, err
	}
	if len(reclaimable) == 1 {
		id := reclaimable[0].ID
		// Re-check expiry in the UPDATE: another allocator may have claimed this
		// row between the read and the write.
		info, err := workers.WithContext(ctx).
			Where(workers.ID.Eq(id), expiredNow).
			UpdateSimple(
				workers.Token.Value(token),
				workers.OS.Value(info.OS), workers.Host.Value(info.Host),
				workers.IPv4Lan.Value(info.IPv4Lan), workers.MAC.Value(info.MAC),
				workers.Disk.Value(info.Disk), workers.Container.Value(info.Container),
				workers.KVM.Value(info.KVM),
				workers.ExpiresAt.SetCol(deadline),
			)
		if err != nil {
			return 0, err
		}
		if info.RowsAffected == 1 {
			return id, nil
		}
	}

	highest, err := workers.WithContext(ctx).Order(workers.ID.Desc()).Limit(1).Find()
	if err != nil {
		return 0, err
	}
	next := 0
	if len(highest) == 1 {
		next = highest[0].ID + 1
	}
	if next > MaxWorkerID {
		return 0, fmt.Errorf("wuid: worker id pool exhausted (max %d)", MaxWorkerID)
	}
	// The lease deadline is a database expression, so the row is inserted with a
	// zero deadline and immediately given its real one. Both statements run
	// under the allocation lock, so no other allocator can observe the gap.
	row := workerRowFor(next, token, info)
	if err := workers.WithContext(ctx).Create(row); err != nil {
		return 0, err
	}
	if _, err := workers.WithContext(ctx).
		Where(workers.ID.Eq(next), workers.Token.Eq(token)).
		UpdateSimple(workers.ExpiresAt.SetCol(deadline)); err != nil {
		return 0, err
	}
	return next, nil
}

// workerRowFor builds the row for a freshly allocated ID. ExpiresAt is left at
// zero and set by the following statement, which can name the database clock.
func workerRowFor(id int, token string, info sysinfo) *model.WorkerRow {
	return &model.WorkerRow{
		ID: id, Token: token, OS: info.OS, Host: info.Host,
		IPv4Lan: info.IPv4Lan, MAC: info.MAC, Disk: info.Disk,
		Container: info.Container, KVM: info.KVM,
	}
}

func (worker *Worker) run(ctx context.Context) {
	defer worker.wg.Done()
	timer := time.NewTimer(worker.renewInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(worker.step(ctx))
	}
}

func (worker *Worker) step(ctx context.Context) time.Duration {
	if !worker.alive.Load() {
		if err := worker.reacquire(ctx); err != nil {
			slog.ErrorContext(ctx, "wuid re-allocation failed", "error", err)
			return worker.retryInterval()
		}
		return worker.renewInterval()
	}

	err := worker.renew(ctx)
	switch {
	case err == nil:
		worker.lastRenew.Store(monotonicMillis())
		return worker.renewInterval()
	case errors.Is(err, errLeaseTakenOver):
		worker.declareLost(fmt.Errorf("wuid: worker id %d taken over", worker.ID()))
		return worker.retryInterval()
	default:
		margin := worker.lease / 3
		// The database clock owns expires_at; this local monotonic clock only
		// stops generation before that lease duration can elapse. Safety assumes
		// clock-rate drift stays within the one-third lease margin. A fixed wall-
		// clock offset between the process and database does not consume it.
		if monotonicMillis()-worker.lastRenew.Load() >= (worker.lease - margin).Milliseconds() {
			worker.declareLost(fmt.Errorf("wuid: could not renew worker id %d: %w", worker.ID(), err))
			return worker.retryInterval()
		}
		slog.ErrorContext(ctx, "wuid renewal failed", "worker_id", worker.ID(), "error", err)
		return worker.retryInterval()
	}
}

func (worker *Worker) renewInterval() time.Duration {
	interval := worker.lease / 3
	if interval <= 0 {
		return time.Second
	}
	return interval
}

func (worker *Worker) retryInterval() time.Duration {
	if interval := worker.renewInterval(); interval < renewRetryInterval {
		return interval
	}
	return renewRetryInterval
}

func (worker *Worker) renew(ctx context.Context) error {
	workers := worker.workers()
	info, err := workers.WithContext(ctx).
		Where(workers.ID.Eq(int(worker.ID())), workers.Token.Eq(worker.leaseToken())).
		UpdateSimple(workers.ExpiresAt.SetCol(
			field.NewUnsafeFieldRaw(worker.nowExpr+" + ?", worker.lease.Milliseconds())))
	if err != nil {
		return err
	}
	if info.RowsAffected != 1 {
		return errLeaseTakenOver
	}
	return nil
}

func (worker *Worker) reacquire(ctx context.Context) error {
	id, token, err := claimUnderLock(ctx, worker.db, worker.lease, worker.nowExpr, worker.tablePrefix)
	if err != nil {
		return err
	}
	worker.id.Store(int64(id))
	worker.generation.Add(1)
	worker.token.Store(token)
	worker.lastRenew.Store(monotonicMillis())
	worker.alive.Store(true)
	slog.InfoContext(ctx, "wuid re-allocated", "worker_id", id)

	worker.mu.Lock()
	callback := worker.onReacquire
	worker.mu.Unlock()
	if callback != nil {
		callback(worker.ID())
	}
	return nil
}

func (worker *Worker) declareLost(err error) {
	if !worker.alive.CompareAndSwap(true, false) {
		return
	}
	slog.Error("wuid lease lost", "worker_id", worker.ID(), "error", err)
	worker.mu.Lock()
	callback := worker.onLost
	worker.mu.Unlock()
	if callback != nil {
		callback(err)
	}
}

func (worker *Worker) Close(ctx context.Context) error {
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	worker.closed = true
	worker.mu.Unlock()
	worker.alive.Store(false)
	if worker.static {
		return nil
	}
	if worker.cancel != nil {
		worker.cancel()
	}
	worker.wg.Wait()
	workers := worker.workers()
	_, err := workers.WithContext(ctx).
		Where(workers.ID.Eq(int(worker.ID())), workers.Token.Eq(worker.leaseToken())).
		UpdateSimple(workers.ExpiresAt.Value(0))
	return err
}

func monotonicMillis() int64 { return time.Since(processStarted).Milliseconds() }
