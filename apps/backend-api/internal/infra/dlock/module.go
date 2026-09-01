package dlock

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

const Name = "dlock"

var ErrDatabaseUnavailable = errors.New("dlock: writable database unavailable")

// Module owns the dlock migration source, runtime access, and health reporting.
type Module struct {
	runtime *appenv.Runtime
	options []Option
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.HealthChecker = (*Module)(nil)
)

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
