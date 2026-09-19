package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
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
	require.NoError(t, module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(h.prefix)))
	require.NoError(t, module.Provide[organization.DirectoryTableNames](services,
		organization.PublishedDirectoryTableNames(h.prefix)))
	require.NoError(t, services.Seal())
	return services
}

func TestGovernanceModuleConstructionAndReadiness(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("governance-test", "1"))
	assert.Panics(t, func() { NewModule(nil, Config{}) })
	instance := NewModule(runtime, Config{})
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{
		platformi18n.Name, guid.Name, audit.Name, core.Name, organization.Name,
	}, instance.ModuleDependencies())
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
	assert.Len(t, governanceOperationManifest(api.OpenAPI()), 12)
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

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
	require.NoError(t, module.Provide[core.RoleGrantStore](services, h.grantStore()))
	require.NoError(t, module.Provide[core.AdministratorGuard](services, governanceAdministratorGuard{}))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "access tables")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
	require.NoError(t, module.Provide[core.RoleGrantStore](services, h.grantStore()))
	require.NoError(t, module.Provide[core.AdministratorGuard](services, governanceAdministratorGuard{}))
	require.NoError(t, module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(h.prefix)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "directory tables")

	var unavailable *Module
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	assert.ErrorContains(t, unavailable.Initialize(t.Context()), "module is required")
	_, api := humatest.New(t)
	assert.ErrorContains(t, unavailable.RegisterHTTP(t.Context(), api), "module is unavailable")

	require.NoError(t, instance.InjectServices(governanceServices(t, h)))
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
}

// TestGovernanceModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. Governance holds its foreign names as
// typed fields now, so a name that never arrived can no longer be reported by
// the statement that needed it: it is caught once, here, and the deployment
// does not start rather than joining an empty table name.
func TestGovernanceModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	for _, missing := range []struct {
		owner     string
		access    core.AccessTableNames
		directory organization.DirectoryTableNames
	}{
		{"core", core.AccessTableNames{}, organization.PublishedDirectoryTableNames("")},
		{"organization", core.PublishedAccessTableNames(""), organization.DirectoryTableNames{}},
		{"both", core.AccessTableNames{}, organization.DirectoryTableNames{}},
	} {
		t.Run(missing.owner, func(t *testing.T) {
			h := newGovernanceHarness(t, "")
			require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
			instance := NewModule(h.runtime, enabledModuleConfig())

			services := module.NewServices()
			require.NoError(t, module.Provide[*authz.Registrar](services, governanceRuntimeRegistrar()))
			require.NoError(t, module.Provide[guid.Source](services, newGovernanceTestIDs(7000)))
			require.NoError(t, module.Provide[audit.Appender](services, h.appendAudit))
			require.NoError(t, module.Provide[core.RoleGrantStore](services, h.grantStore()))
			require.NoError(t, module.Provide[core.AdministratorGuard](services, governanceAdministratorGuard{}))
			require.NoError(t, module.Provide[core.AccessTableNames](services, missing.access))
			require.NoError(t, module.Provide[organization.DirectoryTableNames](services, missing.directory))
			require.NoError(t, services.Seal())

			require.NoError(t, instance.InjectServices(services))
			assert.ErrorContains(t, instance.Initialize(t.Context()),
				"the core and organization modules must publish the table names governance joins")
		})
	}
}
