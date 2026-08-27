package dlock

import (
	"context"
	"errors"
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

var ErrDatabaseUnavailable = errors.New("dlock: writable database unavailable")

// Module owns dlock migration, runtime access, and database health reporting.
type Module struct {
	db      gormx.Writer
	options []Option
}

var (
	_ module.Initializer   = (*Module)(nil)
	_ module.HealthChecker = (*Module)(nil)
)

func New(db gormx.Writer, options ...Option) *Module {
	return &Module{db: db, options: append([]Option(nil), options...)}
}

// Initialize migrates the lock table and creates the runtime locker. A missing
// writer is reported through CheckHealth so the health server can still start.
func (m *Module) Initialize(ctx context.Context) error {
	db := m.writer()
	if db == nil {
		return nil
	}
	if err := Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
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
	return NewLocker(db, m.options...)
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
	if m.db == nil {
		return nil
	}
	return m.db.Write()
}
