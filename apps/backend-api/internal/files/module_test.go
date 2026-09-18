package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
)

func TestModuleProvidesConfiguredExtensibleDriver(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("files-test", "1"))
	m := NewModule(runtime, Config{Config: module.Config{Enabled: true}, Backend: "native-test", Options: DriverConfig{"root": t.TempDir()}})
	require.True(t, m.Enabled())
	require.Equal(t, Name, m.ModuleName())
	require.NoError(t, m.RegisterDriver("native-test", func(c DriverConfig) (Driver, error) { return NewLocal(c["root"]) }))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	driver, err := module.Resolve[Driver](services)
	require.NoError(t, err)
	require.NotNil(t, driver)
	require.NoError(t, m.Initialize(t.Context()))
	require.Error(t, m.RegisterDriver("late", func(c DriverConfig) (Driver, error) { return NewLocal(c["root"]) }))
	require.ErrorIs(t, (&Module{}).Initialize(t.Context()), ErrUnsupported)
	unknown := NewModule(runtime, Config{Backend: "unknown"})
	require.ErrorIs(t, unknown.ProvideServices(module.NewServices()), ErrUnsupported)
}

func TestFileModuleCleanupWorkerLifecycle(t *testing.T) {
	require.Panics(t, func() { NewModule(nil, Config{}) })
	require.ErrorIs(t, (&Module{}).Start(t.Context()), ErrUnsupported)
	require.NoError(t, (&Module{}).Stop(t.Context()))
	runtime := appenv.NewRuntime(appenv.NewIdentity("file-worker-test", "1"))
	m := NewModule(runtime, Config{LocalRoot: t.TempDir()})
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	accounts, err := module.Resolve[*AccountObjects](services)
	require.NoError(t, err)
	require.Same(t, m.accounts, accounts)
	require.NoError(t, m.Start(t.Context()))
	done := m.workerDone
	require.NoError(t, m.Start(t.Context()))
	require.Equal(t, done, m.workerDone, "start must not spawn a second worker")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, m.Stop(ctx))
	require.NoError(t, m.Stop(ctx))
	require.NoError(t, m.Start(t.Context()))
	require.NoError(t, m.Stop(ctx))
}

func TestFileModuleRefusesDuplicateCapabilities(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("file-wiring-test", "1"))
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	accounts, err := NewAccountObjects(driver, "local", "")
	require.NoError(t, err)
	_, err = NewAccountObjects(driver, "local", "invalid-prefix;")
	require.Error(t, err)
	for _, capability := range []string{"driver", "accounts", "legacy"} {
		t.Run(capability, func(t *testing.T) {
			m := NewModule(runtime, Config{})
			services := module.NewServices()
			switch capability {
			case "driver":
				require.NoError(t, module.Provide[Driver](services, driver))
			case "accounts":
				require.NoError(t, module.Provide[*AccountObjects](services, accounts))
			case "legacy":
				require.NoError(t, module.Provide[LegacyImporter](services, LegacyImporter(func(context.Context, string) error { return nil })))
			}
			require.Error(t, m.ProvideServices(services))
		})
	}
}

func TestFileCleanupWorkerExpiresDurableOperationAndReleasesPins(t *testing.T) {
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "worker.sqlite") + "?_foreign_keys=on&_busy_timeout=10000", Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("files-worker", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("worker_"))
	m := NewModule(runtime, Config{LocalRoot: t.TempDir()})
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	sqlDB, err := router.Write().DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "worker_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	tree := directoryTree{prefix: source.Prefix}
	created := time.Now().Add(-2 * time.Hour)
	require.NoError(t, tree.createSpace(t.Context(), router.Write(), 1, 2, 90, "tenant", created))
	_, err = tree.createDirectory(t.Context(), router.Write(), 1, 3, 2, "Source", created)
	require.NoError(t, err)
	_, err = tree.prepareOperation(t.Context(), router.Write(), 100, 90, "interrupted-plan", operationRequest{SpaceID: 1, Action: "copy", DestinationID: 2, Sources: []guid.ID{3}, FilePolicy: "overwrite", DirectoryPolicy: "rename", TypePolicy: "replace"}, operationBudget{100, 1000}, created.Add(time.Hour), created, operationNext(t), operationAllow)
	require.NoError(t, err)
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, m.Stop(ctx))
	})
	require.Eventually(t, func() bool { return operationRecord(t, router.Write(), tree, 100).State == "failed" }, 5*time.Second, 10*time.Millisecond)
	q := query.Bind(router.Write(), tree.prefix)
	pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
	require.NoError(t, err)
	require.Zero(t, pins)
}
