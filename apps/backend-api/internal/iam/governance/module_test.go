package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func enabledModuleConfig() Config {
	return Config{Config: module.Config{Enabled: true}}
}

func TestModuleActivationComesFromConfig(t *testing.T) {
	assert.False(t, (&Module{}).Enabled())
	assert.True(t, (&Module{config: enabledModuleConfig()}).Enabled())
}

type governanceAdministratorGuard struct{}

func (governanceAdministratorGuard) Protect(context.Context, *gorm.DB, guid.ID) (func() error, error) {
	return func() error { return nil }, nil
}

func governanceRuntimeRegistrar() *authz.Registrar {
	return authz.NewRegistrar(
		authz.DefaultRegistry(), governanceHTTPVerifier{}, governanceHTTPPermissions{}, governanceHTTPMembership{},
	)
}

func governanceServices(t *testing.T, h *governanceHarness) *module.Services {
	t.Helper()
	services := module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(7000)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
	require.NoError(t, module.Provide[core.RoleGrantStore](services, h.grantStore()))
	require.NoError(t, module.Provide[core.AdministratorGuard](services, governanceAdministratorGuard{}))
	require.NoError(t, services.Seal())
	return services
}

func TestGovernanceModuleConstructionAndReadiness(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("governance-test", "1"))
	assert.Panics(t, func() { NewModule(nil, Config{}) })
	instance := NewModule(runtime, Config{})
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, core.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
}

func TestGovernanceModuleRuntimeLifecycle(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	instance := NewModule(h.runtime, enabledModuleConfig())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "module is unavailable")
	require.NoError(t, instance.InjectServices(governanceServices(t, h)))
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	_, api = humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, governanceOperationManifest(api.OpenAPI()), 14)
}

func TestGovernanceModuleReportsMissingDependenciesAndInvalidState(t *testing.T) {
	instance := NewModule(appenv.NewRuntime(appenv.NewIdentity("governance-test", "1")), enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "authorization registrar")

	h := newGovernanceHarness(t, "")
	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "audit appender")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "role grant store")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
	require.NoError(t, module.Provide[core.RoleGrantStore](services, h.grantStore()))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "administrator guard")

	var unavailable *Module
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	assert.ErrorContains(t, unavailable.Initialize(t.Context()), "module is required")
	_, api := humatest.New(t)
	assert.ErrorContains(t, unavailable.RegisterHTTP(t.Context(), api), "module is unavailable")

	require.NoError(t, instance.InjectServices(governanceServices(t, h)))
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
}
