// Package wuid allocates a process-unique 16-bit worker ID using either the
// WUID environment variable or a renewable database lease.
package wuid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/infra/dlock"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

const (
	MaxWorkerID        = 1<<16 - 1
	DefaultLease       = time.Hour
	renewRetryInterval = 5 * time.Second
	EnvKey             = "WUID"
	allocLockName      = "wuid:alloc"
)

var (
	ErrDatabaseUnavailable = errors.New("wuid: writable database unavailable")
	errLeaseTakenOver      = errors.New("wuid: lease taken over")
	processStarted         = time.Now()
)

type workerRow struct {
	ID        int    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Token     string `gorm:"column:token;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
	OS        string `gorm:"column:os"`
	Host      string `gorm:"column:host"`
	IPv4Lan   string `gorm:"column:ipv4_lan"`
	MAC       string `gorm:"column:mac"`
	Disk      string `gorm:"column:disk"`
	Container bool   `gorm:"column:container"`
	KVM       bool   `gorm:"column:kvm"`
}

func (workerRow) TableName() string { return "worker_ids" }

type config struct {
	lease       time.Duration
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
	db      *gorm.DB
	nowExpr string
	lease   time.Duration
	static  bool

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
	id, token, err := claimUnderLock(ctx, db, config.lease, nowExpr)
	if err != nil {
		return nil, err
	}

	worker := &Worker{
		db: db, nowExpr: nowExpr, lease: config.lease,
		onLost: config.onLost, onReacquire: config.onReacquire,
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

func claimUnderLock(ctx context.Context, db *gorm.DB, lease time.Duration, nowExpr string) (int, string, error) {
	token, err := newToken()
	if err != nil {
		return 0, "", fmt.Errorf("wuid: create lease token: %w", err)
	}
	lock, err := dlock.NewLocker(db, dlock.WithTTL(lease)).Lock(ctx, allocLockName)
	if err != nil {
		return 0, "", fmt.Errorf("wuid: acquire allocation lock: %w", err)
	}
	id, claimErr := claimID(ctx, db, token, collectSysinfo(), lease.Milliseconds(), nowExpr)
	unlockErr := lock.Unlock(context.WithoutCancel(ctx))
	if claimErr != nil {
		return 0, "", errors.Join(claimErr, unlockErr)
	}
	if unlockErr != nil {
		return 0, "", fmt.Errorf("wuid: release allocation lock: %w", unlockErr)
	}
	return id, token, nil
}

func claimID(ctx context.Context, db *gorm.DB, token string, info sysinfo, leaseMS int64, nowExpr string) (int, error) {
	var expired workerRow
	lookup := db.WithContext(ctx).
		Where("expires_at < " + nowExpr).
		Order("id ASC").
		Limit(1).
		Find(&expired)
	if lookup.Error != nil {
		return 0, lookup.Error
	}
	if lookup.RowsAffected == 1 {
		result := db.WithContext(ctx).Model(&workerRow{}).
			Where("id = ?", expired.ID).
			Where("expires_at < " + nowExpr).
			Updates(workerValues(token, info, gorm.Expr(nowExpr+" + ?", leaseMS)))
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 1 {
			return expired.ID, nil
		}
	}

	var maxID int
	if err := db.WithContext(ctx).Model(&workerRow{}).
		Select("COALESCE(MAX(id), -1)").Scan(&maxID).Error; err != nil {
		return 0, err
	}
	next := maxID + 1
	if next > MaxWorkerID {
		return 0, fmt.Errorf("wuid: worker id pool exhausted (max %d)", MaxWorkerID)
	}
	values := workerValues(token, info, gorm.Expr(nowExpr+" + ?", leaseMS))
	values["id"] = next
	if err := db.WithContext(ctx).Table((workerRow{}).TableName()).Create(values).Error; err != nil {
		return 0, err
	}
	return next, nil
}

func workerValues(token string, info sysinfo, expiresAt any) map[string]any {
	return map[string]any{
		"token": token, "expires_at": expiresAt,
		"os": info.OS, "host": info.Host, "ipv4_lan": info.IPv4Lan,
		"mac": info.MAC, "disk": info.Disk, "container": info.Container, "kvm": info.KVM,
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
	result := worker.db.WithContext(ctx).Model(&workerRow{}).
		Where("id = ?", worker.ID()).
		Where("token = ?", worker.token.Load().(string)).
		UpdateColumn("expires_at", gorm.Expr(worker.nowExpr+" + ?", worker.lease.Milliseconds()))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseTakenOver
	}
	return nil
}

func (worker *Worker) reacquire(ctx context.Context) error {
	id, token, err := claimUnderLock(ctx, worker.db, worker.lease, worker.nowExpr)
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
	return worker.db.WithContext(ctx).Model(&workerRow{}).
		Where("id = ?", worker.ID()).
		Where("token = ?", worker.token.Load().(string)).
		UpdateColumn("expires_at", int64(0)).Error
}

func monotonicMillis() int64 { return time.Since(processStarted).Milliseconds() }
