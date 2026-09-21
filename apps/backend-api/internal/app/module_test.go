package app

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/migrationstatus"
	"context"
	"testing"
	"time"

	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type healthProviderModule struct{}

func (*healthProviderModule) ModuleName() string { return "provider" }
func (*healthProviderModule) HealthCheckers() []healthservice.Checker {
	return []healthservice.Checker{{Name: "provided", Check: func(context.Context) error { return nil }}}
}

func TestMigrationInspectorRejectsUnavailableRuntimeDatabase(t *testing.T) {
	tests := []struct {
		name    string
		runtime *appenv.Runtime
	}{
		{name: "runtime missing"},
		{name: "database missing", runtime: appenv.NewRuntime(appenv.NewIdentity("test", "1"))},
		{name: "writer missing", runtime: appenv.NewRuntime(
			appenv.NewIdentity("test", "1"),
			appenv.WithDatabase(&gormx.DatasourceRouter{}),
		)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := &App{runtime: test.runtime}
			_, err := app.migrationInspector(t.Context())
			assert.ErrorContains(t, err, "writable database is not configured")
		})
	}
}

func TestHealthCheckerProviderReplacesGenericModuleStatus(t *testing.T) {
	app := &App{modules: registeredRegistry(&healthProviderModule{})}
	checkers := app.healthCheckers()
	require.Len(t, checkers, 1)
	assert.Equal(t, "provided", checkers[0].Name)
	assert.NoError(t, checkers[0].Check(t.Context()))
}

func TestProvideCrossCuttingPropagatesDuplicateService(t *testing.T) {
	app := &App{}
	services := module.NewServices()
	require.NoError(t, app.provideCrossCutting(services))
	assert.ErrorContains(t, app.provideCrossCutting(services), "already registered")
}

// registeredRegistry builds a Registry with the given modules, activated only.
func registeredRegistry(mods ...module.Module) *module.Registry {
	registry := module.NewRegistry()
	_ = registry.Register(mods...)
	_ = registry.Activate()
	return registry
}

func TestCrossCuttingProvidersRefuseToWorkWithoutARuntime(t *testing.T) {
	app := &App{}
	services := module.NewServices()
	require.NoError(t, app.provideCrossCutting(services))
	// Publishing the same contract twice is a wiring mistake, not something to
	// silently overwrite, so a second pass over the same services must fail.
	require.Error(t, app.provideCrossCutting(services))
	require.NoError(t, services.Seal())

	// The anonymizer, the health checkers and the migration inspector are all
	// published eagerly at wiring time but resolve lazily. Without a runtime
	// each must refuse rather than operate on a nil database.
	anonymize, err := module.Resolve[identity.PrincipalAnonymizer](services)
	require.NoError(t, err)
	assert.ErrorIs(t, anonymize(t.Context(), nil, identity.Principal{}, time.Now()), identity.ErrUnavailable)

	inspector, err := module.Resolve[migrationstatus.Inspector](services)
	require.NoError(t, err)
	reports, err := inspector(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writable database is not configured")
	assert.Nil(t, reports)

}
