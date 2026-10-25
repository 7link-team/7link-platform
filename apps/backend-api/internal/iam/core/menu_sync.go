package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// MenuJobNamespace is the stable scheduler/outbox namespace for core menu synchronization.
const MenuJobNamespace = "iam_core"
const menuSyncKind = MenuJobNamespace + ".menu_contributions_sync"

type menuSyncRuntime struct {
	queue     jobs.Queue
	leader    jobs.Singleton
	publisher events.Publisher
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
}

func (m *Module) provideMenuContributions(s *module.Services) error {
	m.contributions.registry = m.registrar.Registry()
	return module.Provide[MenuSink](s, &m.contributions)
}

func (m *Module) bindMenuSync(s *module.Services) error {
	q, err := module.Resolve[jobs.Queue](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	l, err := module.Resolve[jobs.Singleton](s)
	if err != nil {
		return err
	}
	p, err := module.Resolve[events.Publisher](s)
	if err != nil {
		return err
	}
	c, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	if err = c.AddTypes(MenuJobNamespace, events.TypeSpec{Name: MenuJobNamespace + ".menus_synced", SchemaVersion: 1, SubjectKind: "menu_owner", Realtime: events.RealtimeSignal}); err != nil {
		return err
	}
	m.menuSync.queue, m.menuSync.leader, m.menuSync.publisher = q, l, p
	return nil
}

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: menuSyncKind, Timeout: 15 * time.Minute, Run: m.runMenuSync}}
}
func (m *Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: menuSyncKind, Kind: menuSyncKind, Interval: 24 * time.Hour, TenantScope: jobs.ScopePlatform}}
}

// Every process participates in the shared renewable leader lease; the startup
// key coalesces restarts for the same declaration content, not for a deployment
// brand or process ID. Daily scheduling also handles administrator deletions.
func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.repository == nil {
		return ErrUnavailable
	}
	if m.menuSync.queue == nil || m.menuSync.leader == nil {
		if len(m.contributions.snapshot()) == 0 {
			return nil
		}
		return ErrUnavailable
	}
	m.contributions.mu.Lock()
	m.contributions.sealed = true
	m.contributions.mu.Unlock()
	m.menuSync.mu.Lock()
	defer m.menuSync.mu.Unlock()
	if m.menuSync.cancel != nil {
		return nil
	}
	run, cancel := context.WithCancel(ctx)
	m.menuSync.cancel = cancel
	m.menuSync.done = make(chan struct{})
	go func() {
		defer close(m.menuSync.done)
		m.menuSync.leader.Run(run, menuSyncKind, func(c context.Context) error {
			_, err := m.menuSync.queue.Enqueue(c, nil, jobs.EnqueueRequest{Kind: menuSyncKind, Payload: map[string]any{}, ConcurrencyKey: menuSyncKind, IdempotencyKey: "startup:" + menuCatalogHash(m.contributions.snapshot()) + ":" + time.Now().UTC().Format(time.DateOnly)})
			return err
		})
	}()
	return nil
}

func menuCatalogHash(pages []ownedMenuPage) string {
	// Catalog declarations are deterministic and already validated; include all
	// versions/fields so an upgrade on the same day gets its own startup pass.
	return menuBytesHash(pages)
}

func (m *Module) Quiesce(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.menuSync.mu.Lock()
	cancel, done := m.menuSync.cancel, m.menuSync.done
	m.menuSync.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) runMenuSync(ctx context.Context, _ jobs.Job) error {
	if m == nil || m.repository == nil || m.appender == nil || m.menuSync.publisher == nil {
		return ErrUnavailable
	}
	r := m.repository
	directory, err := r.directoryTables()
	if err != nil {
		return err
	}
	after := guid.ID(0)
	for {
		var roots []struct {
			ID        guid.ID
			ScopeKind string
		}
		db := r.writer()
		if db == nil {
			return ErrUnavailable
		}
		if err = db.WithContext(ctx).Table(directory.Tenants.String()).Select("id, scope_kind").Where("id > ? AND status = ?", after, "active").Order("id").Limit(100).Find(&roots).Error; err != nil {
			return err
		}
		for _, root := range roots {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				repo := r.withDB(tx)
				changed, e := repo.syncContributedMenus(ctx, root.ID, root.ScopeKind)
				if e != nil || changed == 0 {
					return e
				}
				if e = repo.bumpPolicyRevision(ctx, root.ID); e != nil {
					return e
				}
				auditTenant := root.ID
				if root.ScopeKind == "system" {
					auditTenant = 0
				}
				event := audit.NewDomainEvent(ctx, auditTenant, "menu_contributions_synced", "menu_owner", root.ID)
				event.Detail["changed"], event.Detail["scope_kind"] = changed, root.ScopeKind
				if e = m.appender(ctx, tx, event); e != nil {
					return e
				}
				_, e = m.menuSync.publisher.Publish(ctx, tx, events.Event{TenantID: auditTenant, Type: MenuJobNamespace + ".menus_synced", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:menu_contributions_sync"}, Subject: events.Subject{Kind: "menu_owner", ID: root.ID}, Data: map[string]any{"changed": changed, "scope_kind": root.ScopeKind}})
				return e
			})
			if err != nil {
				return fmt.Errorf("menu contributions owner %s: %w", root.ID, err)
			}
			after = root.ID
		}
		if len(roots) < 100 {
			return nil
		}
	}
}

var (
	_ jobs.HandlerProvider  = (*Module)(nil)
	_ jobs.ScheduleProvider = (*Module)(nil)
	_ module.Starter        = (*Module)(nil)
	_ module.Quiescer       = (*Module)(nil)
)

func (*Module) JobNamespace() string { return MenuJobNamespace }
