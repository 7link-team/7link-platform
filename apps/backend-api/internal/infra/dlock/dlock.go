// Package dlock provides database-backed distributed locks for SQLite, MySQL,
// and PostgreSQL. Each lock is a row with an owner token and a TTL lease.
// Expired leases can be reclaimed, and release only succeeds for the current
// owner, so a stale holder cannot release a lock acquired by another process.
package dlock

//go:generate go run ./dbgen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/infra/dlock/model"
	"7link.com/platform/internal/infra/dlock/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultTTL = 30 * time.Second

var ErrNotAcquired = errors.New("dlock: not acquired")

var ErrMaxHoldExceeded = errors.New("dlock: max hold exceeded")

var ErrInvalidTransaction = errors.New("dlock: invalid transaction or lease request")

// processStarted anchors elapsedMillis to a monotonic origin.
var processStarted = time.Now()

// Locker acquires named distributed locks backed by database rows.
type Locker struct {
	db          *gorm.DB
	tablePrefix string
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

// WithTablePrefix points this locker at a prefixed dlocks table. It must match
// the prefix the migrations ran with, so it comes from the shared runtime
// rather than being set per call site.
func WithTablePrefix(prefix string) Option {
	return func(l *Locker) { l.tablePrefix = prefix }
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

// locks is the only place the dlocks table is named, so a prefixed deployment
// cannot be half-applied: every statement below is built from this handle.
func (l *Locker) locks() *query.LockRows { return query.Bind(l.db, l.tablePrefix) }

// expired is "the lease is past the database's current time". Only the clock
// expression is raw SQL: it is a database function call, not a bindable value,
// so no query builder can express it as a parameter. The column it compares is
// the generated, compile-checked one.
func (l *Locker) expired() field.Expr {
	return l.locks().ExpiresAt.LtCol(field.NewUnsafeFieldRaw(l.nowExpr))
}

// deadline is "the database's current time plus one lease". The TTL is bound as
// a parameter; only the clock expression is raw.
func (l *Locker) deadline() field.Expr {
	return field.NewUnsafeFieldRaw(l.nowExpr+" + ?", l.ttl.Milliseconds())
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

// FenceOn checks the unexpired owner token and locks its row until the caller's
// transaction ends. Call it before any protected read or write, on the same
// database as the lease. PostgreSQL/MySQL use FOR UPDATE; SQLite's transaction
// locking rejects competing writes or a stale snapshot's write upgrade.
// An error requires rollback. Unlock only after committing or rolling back.
func (lk *Lock) FenceOn(ctx context.Context, tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalidTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidTransaction
	}
	if lk == nil || lk.locker == nil || !lk.Alive() {
		return ErrNotAcquired
	}
	bound := *lk.locker
	bound.db = tx
	locks := bound.locks()
	_, err := locks.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(locks.Name.Eq(lk.name), locks.Owner.Eq(lk.owner)).Not(bound.expired()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotAcquired
	}
	return err
}

// Alive reports whether this holder can still guarantee ownership when
// auto-refresh is enabled. Callers should check it before committing
// long-running work. Without auto-refresh, expiry is only observed by Refresh.
func (lk *Lock) Alive() bool { return lk.alive.Load() }

func (l *Locker) newLock(ctx context.Context, name, owner string) *Lock {
	lk := &Lock{locker: l, name: name, owner: owner}
	lk.alive.Store(true)
	lk.lastRenew.Store(elapsedMillis())
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
// is canceled.
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
	locks := l.locks()
	err := locks.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.LockRow{Name: name})
	if err != nil {
		return false, errors.Join(ctx.Err(), err)
	}

	info, err := locks.WithContext(ctx).
		Where(locks.Name.Eq(name)).
		Where(field.Or(l.expired(), locks.Owner.Eq(owner))).
		UpdateSimple(
			locks.Owner.Value(owner),
			locks.ExpiresAt.SetCol(l.deadline()),
		)
	if err != nil {
		return false, errors.Join(ctx.Err(), err)
	}
	// MySQL reports changed rows rather than matched rows unless the DSN sets
	// clientFoundRows. Re-claiming a lock this owner already holds within the
	// same millisecond therefore writes an identical expires_at and reports
	// zero. That reads as "not acquired", which is safe in both directions:
	// Lock retries after a 20ms backoff and succeeds on the next attempt, and
	// TryLock reports ErrNotAcquired for a lock the caller already holds rather
	// than handing out a second claim. Do not relax this to >= 0.
	return info.RowsAffected == 1, nil
}

// Refresh extends the lease if this Lock still owns it.
func (lk *Lock) Refresh(ctx context.Context) error {
	locks := lk.locker.locks()
	info, err := locks.WithContext(ctx).
		Where(locks.Name.Eq(lk.name), locks.Owner.Eq(lk.owner)).
		UpdateSimple(locks.ExpiresAt.SetCol(lk.locker.deadline()))
	if err != nil {
		return err
	}
	if info.RowsAffected != 1 {
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
		lk.lastRenew.Store(elapsedMillis())
		return false
	case errors.Is(err, ErrNotAcquired):
		lk.declareLost(ErrNotAcquired)
		return true
	default:
		margin := lk.locker.ttl / 3
		if elapsedMillis()-lk.lastRenew.Load() >= (lk.locker.ttl - margin).Milliseconds() {
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

	locks := lk.locker.locks()
	_, err := locks.WithContext(ctx).
		Where(locks.Name.Eq(lk.name), locks.Owner.Eq(lk.owner)).
		UpdateSimple(locks.Owner.Value(""), locks.ExpiresAt.Value(0))
	return err
}

// elapsedMillis measures against a process-local monotonic origin, never the
// wall clock. It only ever feeds the renewal grace window, which is an elapsed
// duration: an NTP step must not be able to shorten or extend how long this
// holder believes it has gone without a successful renewal. The database clock
// remains the sole authority on expires_at.
func elapsedMillis() int64 { return time.Since(processStarted).Milliseconds() }

func newOwner() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
