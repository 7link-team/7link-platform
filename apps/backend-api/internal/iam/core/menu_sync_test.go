package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type menuCatalogProbe struct {
	err       error
	namespace string
	specs     []events.TypeSpec
}

func (c *menuCatalogProbe) AddTypes(namespace string, specs ...events.TypeSpec) error {
	c.namespace = namespace
	c.specs = specs
	return c.err
}

func TestMenuSyncBindsOnlyCompleteDependenciesAndRegistersDurableEvent(t *testing.T) {
	for _, fault := range []string{"", "unsealed", "leader", "publisher", "catalog", "registration"} {
		t.Run(fault, func(t *testing.T) {
			m := &Module{}
			s := module.NewServices()
			queue := menuTestQueue{sent: make(chan jobs.EnqueueRequest, 1)}
			catalog := &menuCatalogProbe{}
			failure := errors.New("catalog unavailable")
			if fault == "registration" {
				catalog.err = failure
			}
			require.NoError(t, module.Provide[jobs.Queue](s, queue))
			if fault != "leader" {
				require.NoError(t, module.Provide[jobs.Singleton](s, menuTestLeader{}))
			}
			publisher := menuTestPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
			if fault != "publisher" {
				require.NoError(t, module.Provide[events.Publisher](s, publisher))
			}
			if fault != "catalog" {
				require.NoError(t, module.Provide[events.Catalog](s, catalog))
			}
			if fault != "unsealed" {
				require.NoError(t, s.Seal())
			}
			err := m.bindMenuSync(s)
			switch fault {
			case "unsealed":
				require.ErrorIs(t, err, module.ErrServicesProviding)
			case "leader", "publisher", "catalog":
				require.ErrorIs(t, err, module.ErrServiceMissing)
			case "registration":
				require.ErrorIs(t, err, failure)
			default:
				require.NoError(t, err)
				require.NotNil(t, m.menuSync.queue)
				require.NotNil(t, m.menuSync.leader)
				require.NotNil(t, m.menuSync.publisher)
				require.Equal(t, MenuJobNamespace, catalog.namespace)
				require.Equal(t, []events.TypeSpec{{Name: MenuJobNamespace + ".menus_synced", Module: Name, DisplayNameKey: "iam.activity_event_iam_core_menus_synced_name", DescriptionKey: "iam.activity_event_iam_core_menus_synced_description", SchemaVersion: 1, SubjectKind: "menu_owner", Realtime: events.RealtimeSignal}}, catalog.specs)
				return
			}
			require.Nil(t, m.menuSync.queue, "failed wiring must not publish partial runtime")
		})
	}
}

type menuTestPublisher func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f menuTestPublisher) Publish(c context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return f(c, tx, e)
}

type menuTestQueue struct{ sent chan jobs.EnqueueRequest }

func (q menuTestQueue) Enqueue(_ context.Context, _ *gorm.DB, r jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	q.sent <- r
	return jobs.EnqueueResult{JobID: 1, Created: true}, nil
}
func (menuTestQueue) EnqueueBatch(context.Context, *gorm.DB, []jobs.EnqueueRequest) ([]jobs.EnqueueResult, error) {
	panic("unexpected batch")
}
func (menuTestQueue) Cancel(context.Context, *gorm.DB, guid.ID, string, string) (bool, error) {
	panic("unexpected cancel")
}

type menuTestLeader struct{}

func (menuTestLeader) Run(ctx context.Context, _ string, loop func(context.Context) error) {
	_ = loop(ctx)
}

func TestMenuSyncStartupUsesLeaderQueueAndDrains(t *testing.T) {
	m := &Module{repository: &Repository{}}
	q := menuTestQueue{sent: make(chan jobs.EnqueueRequest, 2)}
	m.menuSync.queue = q
	m.menuSync.leader = menuTestLeader{}
	require.NoError(t, m.Start(t.Context()))
	require.NoError(t, m.Start(t.Context()))
	select {
	case request := <-q.sent:
		require.Equal(t, menuSyncKind, request.Kind)
		require.Equal(t, menuSyncKind, request.ConcurrencyKey)
		require.Contains(t, request.IdempotencyKey, "startup:")
	case <-time.After(3 * time.Second):
		t.Fatal("startup never enqueued")
	}
	require.NoError(t, m.Quiesce(t.Context()))
	require.Empty(t, q.sent)
	require.Error(t, m.contributions.ContributeMenus("late", menuTestPage("late", "/late")))
	pages := []ownedMenuPage{{"test", menuTestPage("one", "/one")}}
	before := menuCatalogHash(pages)
	pages[0].Version++
	require.NotEqual(t, before, menuCatalogHash(pages))
}

func TestMenuSyncJobPagedIdempotentAtomicAuditAndOutbox(t *testing.T) {
	h := newRepositoryHarness(t, "syncjob_")
	r := h.repository
	for i := guid.ID(1); i <= 101; i++ {
		seedTenantAndPrincipal(t, h.db, "syncjob_", i, i+200)
	}
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", menuTestPage("one", "/one")))
	require.NoError(t, h.db.Exec("CREATE TABLE menu_sync_effects (tenant_id BIGINT NOT NULL, kind TEXT NOT NULL)").Error)
	insert := func(tx *gorm.DB, tenant guid.ID, kind string) error {
		return tx.Table("menu_sync_effects").Create(map[string]any{"tenant_id": tenant, "kind": kind}).Error
	}
	fail := true
	m := &Module{repository: r, appender: func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return insert(tx, e.TenantID, "audit")
	}}
	m.menuSync.publisher = menuTestPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		require.Equal(t, "SYSTEM", e.Actor.Kind)
		require.Equal(t, MenuJobNamespace+".menus_synced", e.Type)
		if fail {
			return 0, errors.New("outbox unavailable")
		}
		return 1, insert(tx, e.TenantID, "outbox")
	})
	require.Error(t, m.runMenuSync(t.Context(), jobs.Job{}))
	q := query.BindWithNames(h.db, r.names)
	count, err := q.MenuContributions.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	var effects int64
	require.NoError(t, h.db.Table("menu_sync_effects").Count(&effects).Error)
	require.Zero(t, effects)
	fail = false
	require.NoError(t, m.runMenuSync(t.Context(), jobs.Job{}))
	count, err = q.MenuContributions.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 101, count)
	require.NoError(t, h.db.Table("menu_sync_effects").Count(&effects).Error)
	require.EqualValues(t, 202, effects)
	require.NoError(t, m.runMenuSync(t.Context(), jobs.Job{}))
	require.NoError(t, h.db.Table("menu_sync_effects").Count(&effects).Error)
	require.EqualValues(t, 202, effects)
	require.Equal(t, menuSyncKind, m.JobHandlers()[0].Kind)
	require.Equal(t, jobs.ScopePlatform, m.JobSchedules()[0].TenantScope)
}
