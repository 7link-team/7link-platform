package dlock

import (
	"context"
	"errors"
	"fmt"

	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

const Name = "dlock"

var ErrDatabaseUnavailable = errors.New("dlock: writable database unavailable")

// Module owns the dlock migration source, runtime access, and health reporting.
type Module struct {
	db      gormx.Writer
	options []Option
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.HealthChecker = (*Module)(nil)
)

// NewModule builds the dlock module. The writable datasource is taken from the
// shared runtime; a router without a writer leaves the module degraded (health
// reports ErrDatabaseUnavailable) rather than failing construction.
func NewModule(rt module.Runtime, options ...Option) *Module {
	var db gormx.Writer
	if router, ok := rt.DB.(*gormx.DatasourceRouter); ok {
		db = router
	}
	return newModule(db, options...)
}

// newModule is the package-private construction seam used by tests that inject
// a narrow writer fake.
func newModule(db gormx.Writer, options ...Option) *Module {
	return &Module{db: db, options: append([]Option(nil), options...)}
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
