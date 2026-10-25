package events

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"sync"
	"time"

	"7link.com/platform/internal/infra/dlock"
	eventsi18n "7link.com/platform/internal/infra/events/i18n"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

type Module struct {
	runtime     *appenv.Runtime
	admin       adminAccess
	guard       dlock.TransactionGuard
	acquire     dlock.TryRenewingLease
	subscribers map[string]Subscriber
	onParked    func(context.Context, *gorm.DB, ParkedNotice) error
	metrics     *eventMetrics
	run         *sequencerRun
	budgets     sync.Map
	names       query.TableNames
	ids         guid.Source
	mu          sync.RWMutex
	types       map[string]TypeSpec
	sealed      bool
	now         func() time.Time
}

func NewModule(rt *appenv.Runtime) *Module {
	return &Module{runtime: rt, names: query.BindNames(rt.TablePrefix()), types: map[string]TypeSpec{}, subscribers: map[string]Subscriber{}, metrics: newEventMetrics(), now: func() time.Time { return time.Now().UTC() }}
}
func (*Module) ModuleName() string { return Name }
func (*Module) ModuleDependencies() []string {
	return []string{guid.Name, dlock.Name, platformi18n.Name}
}
func (m *Module) ProvideServices(services *module.Services) error {
	if err := m.provideProjectOccurrences(services); err != nil {
		return err
	}
	if err := m.provideParkedNotifications(services); err != nil {
		return err
	}
	if err := module.Provide[SubscriberCatalog](services, m); err != nil {
		return err
	}
	if err := m.provideAdmin(services); err != nil {
		return err
	}
	if err := module.Provide[EphemeralPublisher](services, m.PublishEphemeral); err != nil {
		return err
	}
	if err := module.Provide[Tail](services, m.Tail); err != nil {
		return err
	}
	if err := module.Provide[Wakeups](services, m.Wakeups); err != nil {
		return err
	}
	if err := module.Provide[EventReader](services, m.ReadEventsOn); err != nil {
		return err
	}
	if err := module.Provide[Publisher](services, m); err != nil {
		return err
	}
	return module.Provide[Catalog](services, m)
}
func (m *Module) InjectServices(services *module.Services) error {
	ids, err := module.Resolve[guid.Source](services)
	if err != nil {
		return err
	}
	m.ids = ids
	m.acquire, err = module.Resolve[dlock.TryRenewingLease](services)
	if err != nil {
		return err
	}
	m.guard, err = module.Resolve[dlock.TransactionGuard](services)
	if err != nil {
		return err
	}
	return nil
}
func (m *Module) Initialize(context.Context) error {
	if m.admin.registrar != nil {
		if err := eventsi18n.Register(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ids == nil {
		return ErrUnavailable
	}
	m.sealed = true
	return nil
}

// AddTypes atomically registers a module's event declarations. Duplicate names
// are errors even if their schemas happen to match: one owner per event type.
func (m *Module) AddTypes(owner string, specs ...TypeSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if !kindPattern.MatchString(owner) {
		return ErrInvalidEvent
	}
	pending := make(map[string]TypeSpec, len(specs))
	for _, spec := range specs {
		if !eventPattern.MatchString(spec.Name) || len(spec.Name) > 96 || !kindPattern.MatchString(spec.SubjectKind) || spec.SchemaVersion < 1 || spec.SchemaVersion > 32767 {
			return ErrInvalidEvent
		}
		switch spec.Realtime {
		case RealtimeNone, RealtimeSignal, RealtimeFull:
		default:
			return ErrInvalidEvent
		}
		if _, exists := m.types[spec.Name]; exists {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidEvent, spec.Name)
		}
		if _, exists := pending[spec.Name]; exists {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidEvent, spec.Name)
		}
		pending[spec.Name] = spec
	}
	for name, spec := range pending {
		m.types[name] = spec
	}
	return nil
}

var (
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Starter                 = (*Module)(nil)
	_ module.Quiescer                = (*Module)(nil)
	_ module.Stopper                 = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
	_ Publisher                      = (*Module)(nil)
	_ Catalog                        = (*Module)(nil)
)
