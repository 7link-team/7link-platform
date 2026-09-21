package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
	"time"
)

func TestFileModuleWiresItsAuthorizationCatalog(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("files-wiring", "1"))
	instance := NewModule(runtime, Config{})
	registry := authz.MustRegistry()
	services := module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewUnboundRegistrar(registry)))
	require.NoError(t, module.Provide[core.EntityConstraintOn](services, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}))
	require.NoError(t, module.Provide[core.TenantConstraintOn](services, func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
	}))
	require.NoError(t, module.Provide[core.AuthorizationSubjectsOn](services, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.AuthorizationSubjects, error) {
		return core.AuthorizationSubjects{}, nil
	}))
	require.NoError(t, module.Provide[core.AuthorizationReferencesOn](services, func(context.Context, *gorm.DB, guid.ID, core.AuthorizationReferences) (bool, error) {
		return false, nil
	}))
	require.NoError(t, module.Provide[core.EntityOwnerResolver](services, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}))
	require.NoError(t, module.Provide[core.DepartmentOwnerResolver](services, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}))
	require.NoError(t, module.Provide[guid.Source](services, &operationServiceIDs{}))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, services.Seal())
	require.Equal(t, []string{guid.Name, audit.Name, core.Name}, instance.ModuleDependencies())
	require.NoError(t, instance.InjectServices(services))
	require.NotNil(t, instance.operations)
	for _, action := range fileAuthorizationActions() {
		registered, ok := registry.Find(action.Code)
		require.True(t, ok, action.Code)
		require.Equal(t, action, registered)
	}
	require.ErrorContains(t, NewModule(runtime, Config{}).InjectServices(services), "already registered")
}

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
	provisioner, err := module.Resolve[core.FileRootProvisioner](services)
	require.NoError(t, err)
	require.NotNil(t, provisioner)
	contentChecker, err := module.Resolve[core.FileRootContentChecker](services)
	require.NoError(t, err)
	require.NotNil(t, contentChecker)
	bindings, err := module.Resolve[*ResourceBindings](services)
	require.NoError(t, err)
	require.Same(t, m.bindings, bindings)
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
	bindings, err := NewResourceBindings("")
	require.NoError(t, err)
	_, err = NewAccountObjects(driver, "local", "invalid-prefix;")
	require.Error(t, err)
	for _, capability := range []string{"driver", "accounts", "bindings", "root-provisioner", "root-content", "legacy"} {
		t.Run(capability, func(t *testing.T) {
			m := NewModule(runtime, Config{})
			services := module.NewServices()
			switch capability {
			case "driver":
				require.NoError(t, module.Provide[Driver](services, driver))
			case "accounts":
				require.NoError(t, module.Provide[*AccountObjects](services, accounts))
			case "bindings":
				require.NoError(t, module.Provide[*ResourceBindings](services, bindings))
			case "root-provisioner":
				require.NoError(t, module.Provide[core.FileRootProvisioner](services, func(context.Context, *gorm.DB, core.FileRootProvisionInput, time.Time) (guid.ID, error) {
					return 0, nil
				}))
			case "root-content":
				require.NoError(t, module.Provide[core.FileRootContentChecker](services, func(context.Context, *gorm.DB, core.FileRootProvisionInput) (bool, error) {
					return false, nil
				}))
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

func TestFileModuleHTTPRegistrationRequiresItsCollaborators(t *testing.T) {
	// RegisterHTTP is the module's transport wiring. Without the operation
	// service or the registrar there is nothing to route to and no guard to
	// enforce, so it must refuse rather than publish unguarded routes.
	for name, module := range map[string]*Module{
		"nil module":    nil,
		"no operations": {registrar: authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry())},
		"no registrar":  {operations: &operationService{}},
	} {
		_, api := humatest.New(t)
		assert.ErrorIs(t, module.RegisterHTTP(t.Context(), api), errOperationUnavailable, name)
		assert.Empty(t, api.OpenAPI().Paths, name)
	}
}
