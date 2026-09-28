package app

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/migrationstatus"
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/internal/principallifecycle"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
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

// systemAuthorityModule stands in for any business module that contributes a
// system-scope action through the shared registry.
type systemAuthorityModule struct{}

const contributedSystemCode = "contributed_console_operate"

func (systemAuthorityModule) ModuleName() string           { return "system-authority-contributor" }
func (systemAuthorityModule) ModuleDependencies() []string { return []string{core.Name} }
func (systemAuthorityModule) InjectServices(services *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](services)
	if err != nil {
		return err
	}
	return registrar.Registry().Contribute([]authz.Action{
		{Resource: "contributed_console", Verb: "operate", Scope: authz.ActionScopeSystem, Summary: "operate the contributed console"},
	}, nil)
}

// TestPrincipalErasureCoversEverySystemScopeCodeInTheSharedRegistry proves the
// erasure platform-access check is driven by the composed app's one registry:
// every system-scope code any module contributed, including one from a module
// core has never heard of, blocks both the guard and the anonymizer.
func TestPrincipalErasureCoversEverySystemScopeCodeInTheSharedRegistry(t *testing.T) {
	app := bootstrapComposedApp(t, systemAuthorityModule{})
	services := app.modules.Services()
	registrar, err := module.Resolve[*authz.Registrar](services)
	require.NoError(t, err)
	registry := registrar.Registry()
	guard, err := module.Resolve[core.PrincipalPlatformAccessGuard](services)
	require.NoError(t, err)

	systemCodes := map[string]bool{}
	for _, action := range registry.All() {
		if action.Scope == authz.ActionScopeSystem {
			systemCodes[action.Code] = true
		}
	}
	for _, code := range []string{contributedSystemCode, "system_file_view", "system_account_view"} {
		require.True(t, systemCodes[code], "the shared registry is missing system-scope code %s", code)
	}

	db := app.runtime.Database().Write()
	const tenantID, roleID, principalID = guid.ID(7001), guid.ID(7101), guid.ID(7201)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec("INSERT INTO iam_roles (tenant_id, id, name, name_key, created_at, updated_at) VALUES (?, ?, 'Holder', 'holder', ?, ?)", tenantID, roleID, now, now).Error)
	require.NoError(t, db.Exec("INSERT INTO iam_role_members (tenant_id, role_id, principal_id, created_at) VALUES (?, ?, ?, ?)", tenantID, roleID, principalID, now).Error)
	grant := func(code string) func() {
		require.NoError(t, db.Exec("INSERT INTO iam_role_permissions (tenant_id, role_id, permission_code, created_at) VALUES (?, ?, ?, ?)", tenantID, roleID, code, now).Error)
		return func() {
			require.NoError(t, db.Exec("DELETE FROM iam_role_permissions WHERE tenant_id = ? AND role_id = ?", tenantID, roleID).Error)
		}
	}

	// Control: an ordinary tenant grant is not platform access, so the check
	// below is refusing because of the code, not because of the fixture.
	revoke := grant("user_view")
	require.NoError(t, guard(t.Context(), db, principalID))
	revoke()

	for code := range systemCodes {
		revoke := grant(code)
		assert.ErrorIs(t, guard(t.Context(), db, principalID), core.ErrPrincipalHasPlatformAccess, code)
		assert.ErrorIs(t, principallifecycle.AnonymizePrincipalData(t.Context(), db, "", registry, identity.Principal{ID: principalID}, now),
			core.ErrPrincipalHasPlatformAccess, code)
		revoke()
	}
}
