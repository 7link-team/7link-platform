package wuid

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("wuid: worker ID unavailable")

const Name = "wuid"

// Module owns the worker ID migration source, allocation, renewal, health, and release.
type Module struct {
	runtime *appenv.Runtime
	options []Option

	mu       sync.RWMutex
	worker   *Worker
	startErr error
}

// Lease identifies the current worker ID lease generation.
type Lease struct {
	WorkerID   uint16
	Generation uint64
}

// Source is the narrow worker ID contract consumed by dependent generators.
type Source interface {
	Current() (Lease, error)
}

func (*Module) ModuleName() string { return Name }

var (
	_ module.Starter         = (*Module)(nil)
	_ module.HealthChecker   = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ Source                 = (*Module)(nil)
)

// NewModule builds the worker ID module. The writable datasource is taken from
// the shared runtime; without a writer the module starts degraded instead of
// failing construction.
func NewModule(rt *appenv.Runtime, options ...Option) *Module {
	return &Module{runtime: rt, options: append([]Option(nil), options...)}
}

func (m *Module) ProvideServices(services *module.Services) error {
	return module.Provide[Source](services, m)
}

func (m *Module) Start(ctx context.Context) error {
	options := []Option{OnLost(m.notifyLost), OnReacquire(m.notifyReacquired)}
	options = append(options, m.options...)
	options = append([]Option{WithTablePrefix(m.runtime.TablePrefix())}, options...)
	worker, err := Open(ctx, m.writer(), options...)
	if err != nil {
		if errors.Is(err, ErrDatabaseUnavailable) {
			m.mu.Lock()
			m.startErr = err
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("allocate worker ID: %w", err)
	}
	m.mu.Lock()
	m.worker = worker
	m.startErr = nil
	m.mu.Unlock()
	return nil
}

func (m *Module) Current() (Lease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.startErr != nil {
		return Lease{}, errors.Join(ErrUnavailable, m.startErr)
	}
	if m.worker == nil || !m.worker.Alive() {
		return Lease{}, ErrUnavailable
	}
	return Lease{WorkerID: m.worker.ID(), Generation: m.worker.Generation()}, nil
}

func (m *Module) ID() (uint16, error) {
	lease, err := m.Current()
	return lease.WorkerID, err
}

func (m *Module) CheckHealth(context.Context) error {
	_, err := m.ID()
	return err
}

func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	worker := m.worker
	m.worker = nil
	m.startErr = ErrUnavailable
	m.mu.Unlock()
	if worker == nil {
		return nil
	}
	return worker.Close(ctx)
}

func (m *Module) notifyLost(err error) {
	m.mu.Lock()
	m.startErr = err
	m.mu.Unlock()
}

func (m *Module) notifyReacquired(uint16) {
	m.mu.Lock()
	m.startErr = nil
	m.mu.Unlock()
}

func (m *Module) writer() *gorm.DB {
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}
