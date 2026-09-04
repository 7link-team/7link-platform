package provisioning

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	platformmodule "7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvisioningModuleConstructionAndDeclarationOnly(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("provisioning-test", "1"))
	registrar := provisioningRuntimeRegistrar()
	assert.Panics(t, func() { NewModule(nil, registrar, Config{}) })
	assert.Panics(t, func() { NewModule(runtime, nil, Config{}) })
	assert.Panics(t, func() {
		NewModule(runtime, authz.NewAuthenticationOnlyRegistrar(provisioningTokenVerifier{}), Config{})
	})
	assert.Panics(t, func() { NewModule(runtime, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()), Config{}) })
	assert.Panics(t, func() { NewDeclarationOnlyModule(nil) })

	declaration := NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	assert.Equal(t, Name, declaration.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, identity.Name, organization.Name}, declaration.ModuleDependencies())
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, declaration.Initialize(t.Context()))
	require.NoError(t, declaration.Start(t.Context()))
	require.NoError(t, declaration.Stop(t.Context()))
	require.NoError(t, declaration.CheckHealth(t.Context()))
	respx.Install()
	_, api := humatest.New(t)
	require.NoError(t, declaration.RegisterHTTP(t.Context(), api))
	assert.Len(t, provisioningOperationManifest(api.OpenAPI()), 30)
	assert.Equal(t, "goose_provisioning", declaration.MigrationSource().TableName)
	assert.Len(t, declaration.MigrationMilestones(), 2)
}

func TestProvisioningModuleDependencyResolutionAndLifecycle(t *testing.T) {
	env := newProvisioningEnvironment(t)
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	config := Config{EncryptionKey: base64.RawStdEncoding.EncodeToString(testProvisioningKey())}
	instance := NewModule(env.runtime, provisioningRuntimeRegistrar(), config)
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "service is unavailable")

	empty := platformmodule.NewServices()
	require.NoError(t, empty.Seal())
	assert.ErrorContains(t, instance.InjectServices(empty), "id generator")

	services := platformmodule.NewServices()
	require.NoError(t, platformmodule.Provide[guid.Source](services, env.service.ids))
	require.NoError(t, platformmodule.Provide[audit.Appender](services, env.service.appender))
	require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, env.service.identity))
	require.NoError(t, platformmodule.Provide[organization.GroupProvisioner](services, env.service.groups))
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	require.NoError(t, instance.Start(t.Context()))
	require.NoError(t, instance.Stop(t.Context()))
	_, api = humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, provisioningOperationManifest(api.OpenAPI()), 30)

	var nilModule *Module
	assert.ErrorContains(t, nilModule.Initialize(t.Context()), "module is required")
	assert.NoError(t, nilModule.Stop(t.Context()))
	assert.ErrorIs(t, (&Module{config: Config{SyncInterval: time.Millisecond}}).Start(t.Context()), ErrUnavailable)
	_, api = humatest.New(t)
	assert.ErrorContains(t, nilModule.RegisterHTTP(t.Context(), api), "module is unavailable")
}

func TestProvisioningModuleReportsMissingDependenciesAndInitializationErrors(t *testing.T) {
	env := newProvisioningEnvironment(t)
	newInstance := func() *Module {
		return NewModule(env.runtime, provisioningRuntimeRegistrar(), Config{})
	}
	servicesWith := func(values ...any) *platformmodule.Services {
		services := platformmodule.NewServices()
		for _, value := range values {
			switch typed := value.(type) {
			case guid.Source:
				require.NoError(t, platformmodule.Provide[guid.Source](services, typed))
			case audit.Appender:
				require.NoError(t, platformmodule.Provide[audit.Appender](services, typed))
			case identity.IdentityProvisioner:
				require.NoError(t, platformmodule.Provide[identity.IdentityProvisioner](services, typed))
			}
		}
		require.NoError(t, services.Seal())
		return services
	}
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(env.service.ids)), "audit appender")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(env.service.ids, env.service.appender)), "identity provisioner")
	assert.ErrorContains(t, newInstance().InjectServices(servicesWith(env.service.ids, env.service.appender, env.service.identity)), "group provisioner")

	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	missing := newInstance()
	assert.ErrorContains(t, missing.Initialize(t.Context()), "runtime database, authz registrar")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	badKey := newInstance()
	badKey.ids, badKey.appender, badKey.identities, badKey.groups = env.service.ids, env.service.appender, env.service.identity, env.service.groups
	badKey.config.EncryptionKey = "short"
	assert.ErrorContains(t, badKey.Initialize(t.Context()), "encryption key")

	timed := &Module{syncCancel: func() {}, syncDone: make(chan struct{})}
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, timed.Stop(stopCtx), context.Canceled)
}

func TestProvisioningModuleSyncLoopReconcilesActiveTenants(t *testing.T) {
	env := newProvisioningEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, 200, "")
	provider.configure(200, "", true)
	target, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Sync", server.URL, "token")
	require.NoError(t, err)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("sync", "sync", "sync@example.test"))
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	before := provider.count()

	instance := NewModule(env.runtime, provisioningRuntimeRegistrar(), Config{SyncInterval: 5 * time.Millisecond})
	instance.ids, instance.appender, instance.identities, instance.groups = env.service.ids, env.service.appender, env.service.identity, env.service.groups
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance.config.EncryptionKey = base64.RawStdEncoding.EncodeToString(testProvisioningKey())
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.Start(t.Context()))
	require.NoError(t, instance.Start(t.Context()))
	require.Eventually(t, func() bool { return provider.count() > before }, time.Second, 10*time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, instance.Stop(stopCtx))
	require.NoError(t, instance.Stop(stopCtx))

	require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_tenants").Error)
	assert.NotPanics(t, func() { instance.reconcileAll(t.Context()) })
}

func TestProvisioningReconcilerHandlesDisabledTargetsAndStorageFailures(t *testing.T) {
	t.Run("disabled target", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		target, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Disabled", "https://example.test", "secret")
		require.NoError(t, err)
		_, err = env.service.ReplaceTarget(t.Context(), testID("tenant-a"), parseGUID(target.Target.ID), "Disabled", "https://example.test", TargetDisabled, "", 1)
		require.NoError(t, err)
		instance := &Module{service: env.service}
		assert.NotPanics(t, func() { instance.reconcileAll(t.Context()) })
	})
	t.Run("target listing failure", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_targets").Error)
		instance := &Module{service: env.service}
		assert.NotPanics(t, func() { instance.reconcileAll(t.Context()) })
	})
	t.Run("target synchronization failure", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		_, err := env.service.CreateTarget(t.Context(), testID("tenant-a"), "Broken", "https://example.test", "secret")
		require.NoError(t, err)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_target_resources").Error)
		instance := &Module{service: env.service}
		assert.NotPanics(t, func() { instance.reconcileAll(t.Context()) })
	})
}

type provisioningTokenVerifier struct{}

func (provisioningTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return nil, nil
}

type provisioningPermissionChecker struct{}

func (provisioningPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (provisioningPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type provisioningMembershipChecker struct{}

func (provisioningMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func provisioningRuntimeRegistrar() *authz.Registrar {
	return authz.NewRegistrar(
		authz.DefaultRegistry(), provisioningTokenVerifier{}, provisioningPermissionChecker{}, provisioningMembershipChecker{},
	)
}
