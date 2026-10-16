// Package jobs owns durable background execution and its shared leader loops.
package jobs

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
	"maps"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
)

const Name = "jobs"

var ErrUnavailable = errors.New("jobs: dependencies unavailable")

// Config defines durable queue admission, concurrency and lease settings.
type Config struct {
	MaxPendingPerTenant, MaxRunningPerTenant int
	Lease                                    time.Duration
	WorkersEnabled                           *bool
	SchedulerEnabled                         *bool
	Queues                                   map[string]int
	PollInterval, DrainTimeout               time.Duration
	ClaimBatch                               int
	Schedules                                map[string]ScheduleOverride
}

// ScheduleOverride supplies deployment-specific schedule timing. A nil interval
// retains the code declaration; an explicit zero is invalid.
type ScheduleOverride struct {
	Interval *time.Duration `mapstructure:"interval"`
}

// Module publishes background execution capabilities through the shared registry.
type Module struct {
	runtime       *appenv.Runtime
	config        Config
	recorder      audit.Recorder
	ids           guid.Source
	guard         dlock.TransactionGuard
	names         query.TableNames
	acquire       dlock.RenewingLease
	tryAcquire    dlock.TryRenewingLease
	mu            sync.RWMutex
	handlers      map[string]Handler
	schedules     map[string]Schedule
	sealed        bool
	onDead        func(context.Context, *gorm.DB, DeadLetter) error
	workers       *workerRun
	leakedWorkers *prometheus.GaugeVec
	activeTenants func(context.Context, guid.ID, int) ([]guid.ID, error)
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	config.Schedules = maps.Clone(config.Schedules)
	for name, override := range config.Schedules {
		if override.Interval != nil {
			interval := *override.Interval
			override.Interval = &interval
			config.Schedules[name] = override
		}
	}
	if config.MaxPendingPerTenant == 0 {
		config.MaxPendingPerTenant = 100000
	}
	if config.MaxRunningPerTenant == 0 {
		config.MaxRunningPerTenant = 4
	}
	if config.Lease == 0 {
		config.Lease = time.Minute
	}
	if config.Queues == nil {
		config.Queues = map[string]int{"default": 8, "notifications": 4}
	} else {
		config.Queues = maps.Clone(config.Queues)
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = 15 * time.Second
	}
	if config.ClaimBatch == 0 {
		config.ClaimBatch = 10
	}
	if config.WorkersEnabled != nil {
		enabled := *config.WorkersEnabled
		config.WorkersEnabled = &enabled
	}
	if config.SchedulerEnabled != nil {
		enabled := *config.SchedulerEnabled
		config.SchedulerEnabled = &enabled
	}
	prefix := ""
	if rt != nil {
		prefix = rt.TablePrefix()
	}
	return &Module{runtime: rt, config: config, names: query.BindNames(prefix), leakedWorkers: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "jobs_leaked_workers", Help: "Handlers still occupying a worker slot after their timeout plus 30-second grace period."}, []string{"queue"})}
}
func (*Module) ModuleName() string           { return Name }
func (*Module) ModuleDependencies() []string { return []string{dlock.Name, guid.Name, audit.Name} }
func (m *Module) ProvideServices(services *module.Services) error {
	if err := module.Provide[Singleton](services, m); err != nil {
		return err
	}
	if err := module.Provide[Catalog](services, m); err != nil {
		return err
	}
	if err := module.Provide[DeadLetterConnector](services, DeadLetterConnector{Bind: m.bindDeadLetter}); err != nil {
		return err
	}
	if err := module.Provide[TenantFanoutConnector](services, TenantFanoutConnector{Bind: m.bindTenantFanout}); err != nil {
		return err
	}
	return module.Provide[Queue](services, m)
}
func (m *Module) InjectServices(services *module.Services) error {
	var err error
	if m.acquire, err = module.Resolve[dlock.RenewingLease](services); err != nil {
		return err
	}
	if m.tryAcquire, err = module.Resolve[dlock.TryRenewingLease](services); err != nil {
		return err
	}
	if m.guard, err = module.Resolve[dlock.TransactionGuard](services); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](services); err != nil {
		return err
	}
	m.recorder, err = module.Resolve[audit.Recorder](services)
	return err
}
func (m *Module) Initialize(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recorder == nil || m.acquire == nil || m.tryAcquire == nil || m.guard == nil || m.ids == nil || m.config.MaxPendingPerTenant < 1 || m.config.MaxRunningPerTenant < 1 || m.config.Lease < time.Millisecond || m.config.Lease%time.Millisecond != 0 {
		return ErrUnavailable
	}
	if m.config.PollInterval <= 0 || m.config.DrainTimeout <= 0 || m.config.ClaimBatch < 1 {
		return ErrInvalidRequest
	}
	for queue, count := range m.config.Queues {
		if !namespacePattern.MatchString(queue) || count < 1 {
			return ErrInvalidRequest
		}
	}
	for _, schedule := range m.schedules {
		if schedule.TenantScope == ScopePerTenant && m.activeTenants == nil {
			return ErrUnavailable
		}
		if _, ok := m.handlers[schedule.Kind]; !ok {
			return fmt.Errorf("%w: schedule %q refers to %q", ErrUnknownKind, schedule.Name, schedule.Kind)
		}
	}
	// Validate all overrides before replacing any declaration, so a rejected
	// configuration cannot leave a partially modified catalog.
	schedules := maps.Clone(m.schedules)
	for name, override := range m.config.Schedules {
		schedule, ok := schedules[name]
		if !ok {
			return fmt.Errorf("%w: unknown schedule override %q", ErrInvalidDeclaration, name)
		}
		if override.Interval != nil {
			if *override.Interval == 0 {
				return fmt.Errorf("%w: schedule override %q has zero interval", ErrInvalidDeclaration, name)
			}
			schedule.Interval = *override.Interval
			schedule.Cron = ""
			var err error
			schedule, err = normalizeSchedule(strings.SplitN(name, ".", 2)[0], schedule)
			if err != nil {
				return err
			}
			schedules[name] = schedule
		}
	}
	m.schedules = schedules
	m.sealed = true
	return nil
}

var (
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Starter                 = (*Module)(nil)
	_ module.Quiescer                = (*Module)(nil)
	_ module.Stopper                 = (*Module)(nil)
	_ Singleton                      = (*Module)(nil)
	_ Catalog                        = (*Module)(nil)
	_ Queue                          = (*Module)(nil)
)
