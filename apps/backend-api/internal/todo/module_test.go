package todo

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	humax "7link.com/platform/pkg/extension/huma"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enabledTodoConfig() Config { return Config{Config: module.Config{Enabled: true}} }

func newTestRuntime(t *testing.T) *appenv.Runtime {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "todo-module.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"), appenv.WithDatabase(router))
	initI18n(t, rt)
	return rt
}

// initI18n starts the platform catalog this module registers into. The
// lifecycle guarantees the ordering through ModuleDependencies; a test that
// exercises Initialize directly has to reproduce it.
func initI18n(t *testing.T, rt *appenv.Runtime) {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
}

// resetI18n clears the catalog between module tests, since Initialize registers
// the todo namespace and RegisterFS rejects a duplicate.

func TestModuleIdentityAndDependencies(t *testing.T) {
	m := NewModule(newTestRuntime(t), enabledTodoConfig())
	assert.Equal(t, Name, m.ModuleName())
	// The module reads messages through i18n and mints identifiers through guid,
	// so both must start first.
	assert.Equal(t, []string{platformi18n.Name, guid.Name, core.Name}, m.ModuleDependencies())
	assert.True(t, m.Enabled())
	assert.False(t, NewModule(nil, Config{}).Enabled())
}

// TestModuleInitializeRegistersLocales is the test that would have caught the
// namespace-prefixed locale keys: the other unit tests never call Initialize,
// so a catalog that fails to register only surfaced once the whole app booted.
//
// Initialize is not idempotent — RegisterFS rejects a duplicate namespace — and
// it does not need to be: the lifecycle runs it exactly once per process.
func TestModuleInitializeRegistersLocales(t *testing.T) {
	m := NewModule(newTestRuntime(t), enabledTodoConfig())
	require.NoError(t, m.InjectServices(servicesWith(t, &stubIDs{})))
	require.NoError(t, m.Initialize(t.Context()))
	require.NotNil(t, m.service, "Initialize must build the service")
}

func TestModuleHealthTracksDatasource(t *testing.T) {
	withDB := NewModule(newTestRuntime(t), enabledTodoConfig())
	require.NoError(t, withDB.InjectServices(servicesWith(t, &stubIDs{})))
	require.NoError(t, withDB.Initialize(t.Context()))
	assert.NoError(t, withDB.CheckHealth(t.Context()))

	// A module that never initialized, or one without a writable datasource,
	// reports unhealthy rather than failing at the first request.
	assert.ErrorIs(t, NewModule(nil, enabledTodoConfig()).CheckHealth(t.Context()), ErrUnavailable)

	bare := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	initI18n(t, bare)
	noDB := NewModule(bare, enabledTodoConfig())
	require.NoError(t, noDB.InjectServices(servicesWith(t, &stubIDs{})))
	require.NoError(t, noDB.Initialize(t.Context()))
	assert.ErrorIs(t, noDB.CheckHealth(t.Context()), ErrUnavailable)
}

func TestModuleInjectServicesRequiresGUID(t *testing.T) {
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.Error(t, NewModule(newTestRuntime(t), enabledTodoConfig()).InjectServices(services),
		"a missing identifier source must fail wiring, not surface at the first create")
}

func TestModuleRegisterHTTPRequiresInitialize(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))

	uninitialized := NewModule(newTestRuntime(t), enabledTodoConfig())
	assert.Error(t, uninitialized.RegisterHTTP(context.Background(), api),
		"registering routes before Initialize must fail loudly")

	ready := NewModule(newTestRuntime(t), enabledTodoConfig())
	require.NoError(t, ready.InjectServices(servicesWith(t, &stubIDs{})))
	require.NoError(t, ready.Initialize(t.Context()))
	assert.NoError(t, ready.RegisterHTTP(context.Background(), api))
}

// TestRegisterHTTPWithoutADatasourceWarnsAndStillRegisters pins the one place
// this module degrades instead of failing.
//
// Registration runs once per process, so a boot without a writable datasource
// decides the API document for the rest of that process's life: the filter
// parameters are built from the generated column set, which needs a datasource
// to construct. Failing here would turn a module every route already reports as
// unavailable into a server that will not start, so the routes register anyway
// — but a degradation nobody can see is the kind that reaches production, which
// is why the warning is asserted alongside the success.
func TestRegisterHTTPWithoutADatasourceWarnsAndStillRegisters(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	bare := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	initI18n(t, bare)
	noDB := NewModule(bare, enabledTodoConfig())
	require.NoError(t, noDB.InjectServices(servicesWith(t, &stubIDs{})))
	require.NoError(t, noDB.Initialize(t.Context()))

	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	require.NoError(t, noDB.RegisterHTTP(context.Background(), api),
		"a module with no datasource must still register its routes")

	assert.Contains(t, logged.String(), "todo filters are missing from the API document",
		"the degraded document must be reported, not silently served")

	// The routes exist; only their documentation is thin.
	operation := api.OpenAPI().Paths["/entities/{entity_id}"+basePath].Get
	require.NotNil(t, operation)
	for _, param := range operation.Parameters {
		assert.NotEqual(t, "status", param.Name, "no filter should be documented without a contract")
	}
}

func TestModuleMigrationSourceIsSelfDescribing(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_todo", source.TableName)
	assert.NotNil(t, source.FS)
	// The prefix is injected by the migration runner, not declared here.
	assert.Empty(t, source.Prefix)
}

// servicesWith builds a sealed service registry offering an identifier source,
// mirroring what the lifecycle hands a module during Wire.
func servicesWith(t *testing.T, ids guid.Source) *module.Services {
	t.Helper()
	services := module.NewServices()
	require.NoError(t, module.Provide(services, ids))
	policy := &todoPolicy{}
	require.NoError(t, module.Provide[core.AuthorizationEvaluator](services, policy))
	require.NoError(t, module.Provide[core.MemberDepartmentReader](services, policy))
	require.NoError(t, module.Provide[core.EntityOwnerResolver](services, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 1, EntityID: 10}, true, nil
	}))
	// The core catalog, as IAM core publishes it: the module contributes its own.
	require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewRegistrar(authz.DefaultRegistry(), todoVerifier{}, policy, todoVerifier{})))
	require.NoError(t, services.Seal())
	return services
}

func TestModuleContributesItsPermissionsToTheSharedRegistry(t *testing.T) {
	services := servicesWith(t, &stubIDs{})
	registrar, err := module.Resolve[*authz.Registrar](services)
	require.NoError(t, err)
	for _, action := range authorizationActions() {
		_, found := registrar.Registry().Find(authz.PermissionCode(action.Resource, action.Verb))
		require.False(t, found, "the core catalog must not declare %s", action.Resource+"_"+action.Verb)
	}
	require.Empty(t, registrar.Registry().Fields(Name))

	require.NoError(t, NewModule(newTestRuntime(t), enabledTodoConfig()).InjectServices(services))
	for _, action := range authorizationActions() {
		registered, found := registrar.Registry().Find(authz.PermissionCode(action.Resource, action.Verb))
		require.True(t, found, action.Verb)
		assert.Equal(t, action.Summary, registered.Summary)
		assert.True(t, registered.DataScoped)
	}
	assert.Len(t, registrar.Registry().Fields(Name), len(authorizationFields()))
	description, found := registrar.Registry().FindField(Name, "description")
	require.True(t, found)
	assert.True(t, description.Maskable)

	// A second contribution of the same codes is a wiring error, not a silent merge.
	assert.ErrorContains(t, NewModule(newTestRuntime(t), enabledTodoConfig()).InjectServices(services), "duplicate authz code")
}

func TestModuleContributesTheRawQueryMiddleware(t *testing.T) {
	// The list handler parses filters from the request's query rather than from
	// declared fields, so the module must contribute the middleware that makes
	// it reachable.
	var reached bool
	handler := NewModule(nil, enabledTodoConfig()).HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached = rawQueryFrom(r.Context()) != nil
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/todos?a=eq:1", nil))
	assert.True(t, reached)
}
