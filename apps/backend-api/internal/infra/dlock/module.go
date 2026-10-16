package dlock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

const Name = "dlock"

var ErrDatabaseUnavailable = errors.New("dlock: writable database unavailable")

// TransactionLease acquires a non-renewing lease. Before doing protected work,
// the caller must FenceOn its SQL transaction, then Unlock after that transaction
// ends. Contention returns ErrNotAcquired without waiting.
type TransactionLease func(context.Context, string, time.Duration) (*Lock, error)

// RenewingLease waits for ownership and renews it until Unlock. onLost must
// promptly cancel the protected work; it must not call Unlock synchronously.
type RenewingLease func(context.Context, string, time.Duration, func(string, error)) (*Lock, error)

// TryRenewingLease attempts ownership once, returning ErrNotAcquired on
// contention. Successful leases renew until Unlock; onLost cancels protected work.
// As with RenewingLease, onLost must not call Unlock synchronously.
type TryRenewingLease func(context.Context, string, time.Duration, func(string, error)) (*Lock, error)

// TransactionGuard serializes a short database operation until its transaction
// commits or rolls back. It has no renewable lease and needs no explicit unlock.
// Acquire multiple guards in sorted name order, before protected reads/writes.
type TransactionGuard func(context.Context, *gorm.DB, string) error

// Module owns the dlock migration source, runtime access, and health reporting.
type Module struct {
	runtime *appenv.Runtime
	options []Option
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.HealthChecker   = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
)

func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[TransactionLease](services, m.transactionLease); err != nil {
		return err
	}
	if err := module.Provide[RenewingLease](services, m.renewingLease); err != nil {
		return err
	}
	if err := module.Provide[TryRenewingLease](services, m.tryRenewingLease); err != nil {
		return err
	}
	return module.Provide[TransactionGuard](services, m.transactionGuard)
}

func (m *Module) renewingLease(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*Lock, error) {
	return m.acquireRenewing(ctx, name, ttl, onLost, true)
}
func (m *Module) tryRenewingLease(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*Lock, error) {
	return m.acquireRenewing(ctx, name, ttl, onLost, false)
}
func (m *Module) acquireRenewing(ctx context.Context, name string, ttl time.Duration, onLost func(string, error), wait bool) (*Lock, error) {
	if name == "" || ttl <= 0 || onLost == nil {
		return nil, ErrInvalidTransaction
	}
	locker := m.Locker()
	if locker == nil {
		return nil, ErrDatabaseUnavailable
	}
	locker.ttl, locker.autoRefresh, locker.onLost = ttl, true, onLost
	if wait {
		return locker.Lock(ctx, name)
	}
	return locker.TryLock(ctx, name)
}

func (m *Module) transactionLease(ctx context.Context, name string, ttl time.Duration) (*Lock, error) {
	if name == "" || ttl <= 0 {
		return nil, ErrInvalidTransaction
	}
	locker := m.Locker()
	if locker == nil {
		return nil, ErrDatabaseUnavailable
	}
	locker.ttl = ttl
	// SQL row locking fences the transaction. A separate renewal would contend
	// with that same row and can deadlock on a single-connection writer pool.
	locker.autoRefresh = false
	return locker.TryLock(ctx, name)
}

// NewModule builds the dlock module. The writable datasource is taken from the
// shared runtime; a router without a writer leaves the module degraded (health
// reports ErrDatabaseUnavailable) rather than failing construction.
func NewModule(rt *appenv.Runtime, options ...Option) *Module {
	return &Module{runtime: rt, options: append([]Option(nil), options...)}
}

func (m *Module) CheckHealth(ctx context.Context) error {
	db := m.writer()
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("dlock: get database pool: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("dlock: ping database: %w", err)
	}
	return nil
}

// Locker selects a writer for one lock lifecycle. The returned lock keeps that
// writer for acquisition, refresh, and release so one lease cannot cross
// database instances.
func (m *Module) Locker() *Locker {
	db := m.writer()
	if db == nil {
		return nil
	}
	// The runtime prefix goes first so an explicit WithTablePrefix option, if a
	// caller ever passes one, still takes effect.
	options := append([]Option{WithTablePrefix(m.runtime.TablePrefix())}, m.options...)
	return NewLocker(db, options...)
}

func (m *Module) TryLock(ctx context.Context, name string) (*Lock, error) {
	locker := m.Locker()
	if locker == nil {
		return nil, ErrDatabaseUnavailable
	}
	return locker.TryLock(ctx, name)
}

func (m *Module) Lock(ctx context.Context, name string) (*Lock, error) {
	locker := m.Locker()
	if locker == nil {
		return nil, ErrDatabaseUnavailable
	}
	return locker.Lock(ctx, name)
}

func (m *Module) writer() *gorm.DB {
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}
