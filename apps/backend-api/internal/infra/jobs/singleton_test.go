package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func singletonHarness(t *testing.T, handlers ...Handler) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "jobs.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	source := (&dlock.Module{}).MigrationSource()
	source.Prefix = "j_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("j_"))
	locks := dlock.NewModule(rt)
	m := NewModule(rt, Config{})
	services := module.NewServices()
	require.NoError(t, locks.ProvideServices(services))
	ids, err := guid.New(1)
	require.NoError(t, err)
	require.NoError(t, module.Provide[guid.Source](services, ids))
	require.NoError(t, module.Provide[audit.Recorder](services, &executionRecorder{}))
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, m.AddHandlers("jobs", handlers...))
	require.NoError(t, m.Initialize(t.Context()))
	singleton, err := module.Resolve[Singleton](services)
	require.NoError(t, err)
	require.Same(t, m, singleton)
	return m, db
}

func TestSingletonExclusiveOwnershipAndHandoff(t *testing.T) {
	m, _ := singletonHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	done := make(chan struct{})
	var running atomic.Int32
	go func() {
		defer close(done)
		m.Run(ctx, "jobs:scheduler", func(ctx context.Context) error {
			running.Add(1)
			defer running.Add(-1)
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("leader not started")
	}
	contenderCtx, contenderCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer contenderCancel()
	m.Run(contenderCtx, "jobs:scheduler", func(context.Context) error { t.Error("two leaders ran concurrently"); return nil })
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("leader did not stop")
	}
	calls := 0
	m.Run(t.Context(), "jobs:scheduler", func(context.Context) error { require.Zero(t, running.Load()); calls++; return nil })
	require.Equal(t, 1, calls)
}

func TestSingletonLossCancelsAndRetries(t *testing.T) {
	m, db := singletonHarness(t)
	original := m.acquire
	m.acquire = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
		require.Equal(t, 30*time.Second, ttl)
		return original(ctx, name, 90*time.Millisecond, onLost)
	}
	calls := 0
	m.Run(t.Context(), "jobs:scheduler", func(ctx context.Context) error {
		calls++
		if calls == 1 {
			require.NoError(t, db.Exec("DELETE FROM j_dlocks WHERE name = ?", "jobs:scheduler").Error)
			select {
			case <-ctx.Done():
				require.ErrorIs(t, context.Cause(ctx), dlock.ErrNotAcquired)
			case <-time.After(3 * time.Second):
				t.Fatal("lost leader was not canceled")
			}
			return nil // Cancellation still prevents treating this as successful completion.
		}
		return nil
	})
	require.Equal(t, 2, calls)
}

func TestSingletonFailureRetryAndPanicRelease(t *testing.T) {
	m, _ := singletonHarness(t)
	original := m.acquire
	attempts := 0
	m.acquire = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("database temporarily unavailable")
		}
		if attempts == 2 {
			return nil, nil
		}
		return original(ctx, name, ttl, onLost)
	}
	calls := 0
	m.Run(t.Context(), "jobs:scheduler", func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("retry loop")
		}
		return nil
	})
	require.Equal(t, 4, attempts)
	require.Equal(t, 2, calls)
	require.Panics(t, func() { m.Run(t.Context(), "jobs:scheduler", func(context.Context) error { panic("loop panic") }) })
	m.Run(t.Context(), "jobs:scheduler", func(context.Context) error { calls++; return nil })
	require.Equal(t, 3, calls)
}

func TestSingletonCancellationAndInvalidDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := &Module{acquire: func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		t.Fatal("canceled run acquired lock")
		return nil, nil
	}}
	m.Run(ctx, "leader", func(context.Context) error { t.Fatal("canceled run called loop"); return nil })
	for _, owner := range []*Module{nil, {}} {
		owner.Run(t.Context(), "leader", func(context.Context) error { t.Fatal("unwired run called loop"); return nil })
	}
	m.Run(t.Context(), "", func(context.Context) error { return nil })
	m.Run(t.Context(), "leader", nil)
	require.Equal(t, Name, m.ModuleName())
	require.Equal(t, []string{dlock.Name, guid.Name, audit.Name, i18n.Name}, m.ModuleDependencies())
	unwired := NewModule(nil, Config{})
	require.ErrorIs(t, unwired.Initialize(t.Context()), ErrUnavailable)
	services := module.NewServices()
	require.NoError(t, services.Seal())
	require.Error(t, unwired.InjectServices(services))
	ctx, cancel = context.WithCancel(t.Context())
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		cancel()
		return nil, context.Canceled
	}
	m.Run(ctx, "leader", func(context.Context) error { t.Fatal("failed acquire called loop"); return nil })
}

func TestSingletonCancellationAtAcquisitionAndDuringBackoff(t *testing.T) {
	m, _ := singletonHarness(t)
	original := m.acquire
	ctx, cancel := context.WithCancel(t.Context())
	m.acquire = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
		lease, err := original(ctx, name, ttl, onLost)
		cancel()
		return lease, err
	}
	m.Run(ctx, "leader", func(context.Context) error { t.Fatal("loop started after cancellation during acquisition"); return nil })
	// The acquired lease must still have been released.
	m.acquire = original
	calls := 0
	m.Run(t.Context(), "leader", func(context.Context) error { calls++; return nil })
	require.Equal(t, 1, calls)
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, errors.New("database unavailable")
	}
	m.Run(ctx, "leader", func(context.Context) error { t.Fatal("loop started without lease"); return nil })
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
