package realtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

func eventRealtimeHarness(t *testing.T) (*Module, *events.Module) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "stream.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("rt_"))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	outbox := events.NewModule(rt)
	locks := dlock.NewModule(rt)
	raw, err := router.Write().DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{locks.MigrationSource(), outbox.MigrationSource()} {
		source.Prefix = "rt_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	ids, err := guid.New(1)
	require.NoError(t, err)
	v := &streamVerifier{}
	r := authz.NewRegistrar(authz.MustRegistry(), v, v, v)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, module.Provide[*authz.Registrar](s, r))
	require.NoError(t, locks.ProvideServices(s))
	require.NoError(t, outbox.ProvideServices(s))
	require.NoError(t, m.ProvideServices(s))
	require.NoError(t, s.Seal())
	require.NoError(t, outbox.InjectServices(s))
	require.NoError(t, m.InjectServices(s))
	require.NoError(t, outbox.Initialize(t.Context()))
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, outbox.Start(t.Context()))
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, m.Stop(context.Background()))
		require.NoError(t, outbox.Stop(context.Background()))
	})
	return m, outbox
}

func TestRealtimeModuleLifecycleAndDeclarations(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	require.Equal(t, Name, m.ModuleName())
	require.False(t, m.Enabled())
	require.Equal(t, []string{events.Name, core.Name}, m.ModuleDependencies())
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
	require.ErrorIs(t, m.Start(t.Context()), ErrUnavailable)
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.Error(t, m.ProvideServices(s))
	require.NoError(t, s.Seal())
	require.Error(t, m.InjectServices(s))
	require.NotNil(t, m.Collector())
	require.ErrorIs(t, m.RegisterHTTP(t.Context(), nil), ErrUnavailable)
	require.NoError(t, m.Stop(t.Context()))
	m, _ = eventRealtimeHarness(t)
	require.True(t, m.Enabled())
	require.Error(t, m.register("project", nil))
	require.NoError(t, m.Start(t.Context()))
	require.NoError(t, m.Stop(t.Context()))
	require.ErrorIs(t, m.Start(t.Context()), ErrUnavailable)
}

func TestHubStartErrorsArePropagated(t *testing.T) {
	h := newHub()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, h.start(ctx, nil, nil), context.Canceled)
	require.ErrorIs(t, h.start(t.Context(), func(context.Context, int64, int, bool) (events.TailPage, error) {
		return events.TailPage{}, ErrUnavailable
	}, nil), ErrUnavailable)
	require.NoError(t, h.quiesce(t.Context()))
}

func TestRealtimeDependencyInjectionFailsBeforeExposingPartialServices(t *testing.T) {
	m, _ := eventRealtimeHarness(t)
	providers := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[*authz.Registrar](s, m.registrar) },
		func(s *module.Services) error { return module.Provide[events.Tail](s, m.tail) },
		func(s *module.Services) error { return module.Provide[events.Wakeups](s, m.wake) },
		func(s *module.Services) error { return module.Provide[events.EphemeralPublisher](s, m.publisher) },
	}
	for limit := 0; limit < len(providers); limit++ {
		s := module.NewServices()
		for _, provide := range providers[:limit] {
			require.NoError(t, provide(s))
		}
		require.NoError(t, s.Seal())
		candidate := NewModule(m.runtime, Config{})
		require.Error(t, candidate.InjectServices(s))
	}
	for _, provide := range []func(*module.Services) error{func(s *module.Services) error { return module.Provide[Broadcaster](s, m.Broadcast) }, func(s *module.Services) error { return module.Provide[TopicAuthorizers](s, TopicAuthorizers{}) }} {
		s := module.NewServices()
		require.NoError(t, provide(s))
		require.Error(t, NewModule(m.runtime, Config{}).ProvideServices(s))
	}
	degraded := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	degraded.sealed = true
	require.NoError(t, degraded.Start(t.Context()))
	require.ErrorIs(t, degraded.Broadcast(t.Context(), "tenant/1/projects", nil), ErrUnavailable)
}

func TestReplicaDatabaseBroadcastCoalescesAndWakesInternalSubscription(t *testing.T) {
	m, outbox := eventRealtimeHarness(t)
	other := newHub()
	require.NoError(t, other.start(t.Context(), outbox.Tail, func(context.Context) {}))
	t.Cleanup(func() { require.NoError(t, other.quiesce(context.Background())) })
	topic := "tenant/1/machine/2/channel"
	signals, stop := other.Subscribe(t.Context(), []string{topic})
	defer stop()
	// Publish multiple updates inside one window: the latest payload wins.
	require.NoError(t, m.Broadcast(t.Context(), topic, map[string]any{"value": "first"}))
	require.NoError(t, m.Broadcast(t.Context(), topic, map[string]any{"value": "latest"}))
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatal("second replica did not observe the persisted broadcast")
	}
	page, err := outbox.Tail(t.Context(), 0, 100, false)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, events.RealtimeFull, page.Events[0].Mode)
	require.JSONEq(t, `{"payload":{"value":"latest"}}`, string(page.Events[0].Data))
	require.Contains(t, page.Events[0].Topics, topic)
	// Collection broadcasts use a valid positive subject even without item ID.
	require.NoError(t, m.Broadcast(t.Context(), "tenant/1/machines", map[string]int{"count": 2}))
	m.flushBroadcasts(t.Context())
	_, err = outbox.Sequence(t.Context())
	require.NoError(t, err)
	page, err = outbox.Tail(t.Context(), 1, 100, false)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
}
