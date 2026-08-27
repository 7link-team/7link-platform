// Package dlock provides database-backed distributed locks for SQLite, MySQL,
// and PostgreSQL. Each lock is a row with an owner token and a TTL lease.
// Expired leases can be reclaimed, and release only succeeds for the current
// owner, so a stale holder cannot release a lock acquired by another process.
package dlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultTTL = 30 * time.Second

var ErrNotAcquired = errors.New("dlock: not acquired")

var ErrMaxHoldExceeded = errors.New("dlock: max hold exceeded")

type lockRow struct {
	Name      string `gorm:"column:name;primaryKey;size:191"`
	Owner     string `gorm:"column:owner;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
}

func (lockRow) TableName() string { return "dlocks" }

// Locker acquires named distributed locks backed by database rows.
type Locker struct {
	db          *gorm.DB
	nowExpr     string
	ttl         time.Duration
	autoRefresh bool
	maxHold     time.Duration
	onLost      func(name string, err error)
}

type Option func(*Locker)

// WithTTL sets the lease duration. Non-positive values are ignored.
func WithTTL(ttl time.Duration) Option {
	return func(l *Locker) {
		if ttl > 0 {
			l.ttl = ttl
		}
	}
}

// WithAutoRefresh renews held locks in the background until Unlock is called.
func WithAutoRefresh() Option {
	return func(l *Locker) { l.autoRefresh = true }
}

// WithMaxHold caps how long an auto-refreshed lock is kept alive. Zero means
// there is no cap.
func WithMaxHold(d time.Duration) Option {
	return func(l *Locker) { l.maxHold = d }
}

// WithOnLost registers a callback for an auto-refreshed lock that can no
// longer guarantee ownership.
func WithOnLost(fn func(name string, err error)) Option {
	return func(l *Locker) { l.onLost = fn }
}

// NewLocker creates a Locker using db. Unsupported database dialects panic because
// lease expiry cannot safely fall back to an application node's local clock.
func NewLocker(db *gorm.DB, opts ...Option) *Locker {
	l := &Locker{
		db:      db,
		nowExpr: gormx.NowMillisExpr(db.Dialector.Name()),
		ttl:     DefaultTTL,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Lock is a currently held distributed lock.
type Lock struct {
	locker *Locker
	name   string
	owner  string

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	alive     atomic.Bool
	lastRenew atomic.Int64

	mu     sync.Mutex
	closed bool
}

func (lk *Lock) Name() string { return lk.name }

func (lk *Lock) Owner() string { return lk.owner }

// Alive reports whether this holder can still guarantee ownership when
// auto-refresh is enabled. Callers should check it before committing
// long-running work. Without auto-refresh, expiry is only observed by Refresh.
func (lk *Lock) Alive() bool { return lk.alive.Load() }

func (l *Locker) newLock(ctx context.Context, name, owner string) *Lock {
	lk := &Lock{locker: l, name: name, owner: owner}
	lk.alive.Store(true)
	lk.lastRenew.Store(nowMillis())
	if l.autoRefresh {
		refreshCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		lk.cancel = cancel
		lk.startRefresh(refreshCtx)
	}
	return lk
}

// TryLock attempts to acquire name once. It returns ErrNotAcquired when a
// non-expired lease is held by another owner.
func (l *Locker) TryLock(ctx context.Context, name string) (*Lock, error) {
	owner := newOwner()
	ok, err := l.acquire(ctx, name, owner)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotAcquired
	}
	return l.newLock(ctx, name, owner), nil
}

// Lock retries with bounded exponential backoff until name is acquired or ctx
// is cancelled.
func (l *Locker) Lock(ctx context.Context, name string) (*Lock, error) {
	owner := newOwner()
	const (
		minBackoff = 20 * time.Millisecond
		maxBackoff = 500 * time.Millisecond
	)
	backoff := minBackoff

	for {
		ok, err := l.acquire(ctx, name, owner)
		if err != nil {
			return nil, err
		}
		if ok {
			return l.newLock(ctx, name, owner), nil
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// acquire first creates an expired placeholder, then atomically claims it when
// it is free, expired, or already owned by this retry loop.
func (l *Locker) acquire(ctx context.Context, name, owner string) (bool, error) {
	db := l.db.WithContext(ctx)
	err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&lockRow{
		Name: name,
	}).Error
	if err != nil {
		return false, errors.Join(ctx.Err(), err)
	}

	tx := db.Model(&lockRow{}).
		Where("name = ?", name).
		Where("(expires_at < "+l.nowExpr+" OR owner = ?)", owner).
		Updates(map[string]any{
			"owner":      owner,
			"expires_at": gorm.Expr(l.nowExpr+" + ?", l.ttl.Milliseconds()),
		})
	if tx.Error != nil {
		return false, errors.Join(ctx.Err(), tx.Error)
	}
	return tx.RowsAffected == 1, nil
}

// Refresh extends the lease if this Lock still owns it.
func (lk *Lock) Refresh(ctx context.Context) error {
	tx := lk.locker.db.WithContext(ctx).Model(&lockRow{}).
		Where("name = ?", lk.name).
		Where("owner = ?", lk.owner).
		UpdateColumn("expires_at", gorm.Expr(lk.locker.nowExpr+" + ?", lk.locker.ttl.Milliseconds()))
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected != 1 {
		return ErrNotAcquired
	}
	return nil
}

func (lk *Lock) startRefresh(ctx context.Context) {
	interval := lk.locker.ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	acquiredAt := time.Now()

	lk.wg.Add(1)
	go func() {
		defer lk.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if lk.refreshTick(ctx, acquiredAt) {
					return
				}
			}
		}
	}()
}

func (lk *Lock) refreshTick(ctx context.Context, acquiredAt time.Time) bool {
	if lk.locker.maxHold > 0 && time.Since(acquiredAt) >= lk.locker.maxHold {
		lk.declareLost(ErrMaxHoldExceeded)
		return true
	}

	err := lk.Refresh(ctx)
	switch {
	case err == nil:
		lk.lastRenew.Store(nowMillis())
		return false
	case errors.Is(err, ErrNotAcquired):
		lk.declareLost(ErrNotAcquired)
		return true
	default:
		margin := lk.locker.ttl / 3
		if nowMillis()-lk.lastRenew.Load() >= (lk.locker.ttl - margin).Milliseconds() {
			lk.declareLost(err)
			return true
		}
		return false
	}
}

func (lk *Lock) declareLost(err error) {
	lk.alive.Store(false)
	if fn := lk.locker.onLost; fn != nil {
		fn(lk.name, err)
	}
}

// Unlock stops auto-refresh and clears the lease only if it is still owned by
// this Lock. It is safe to call more than once.
func (lk *Lock) Unlock(ctx context.Context) error {
	lk.mu.Lock()
	if lk.closed {
		lk.mu.Unlock()
		return nil
	}
	lk.closed = true
	lk.mu.Unlock()

	lk.alive.Store(false)
	if lk.cancel != nil {
		lk.cancel()
	}
	lk.wg.Wait()

	return lk.locker.db.WithContext(ctx).Model(&lockRow{}).
		Where("name = ?", lk.name).
		Where("owner = ?", lk.owner).
		Updates(map[string]any{"owner": "", "expires_at": int64(0)}).Error
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func newOwner() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
