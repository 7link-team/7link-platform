package core

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
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

func TestModuleIdentityDependenciesAndReadiness(t *testing.T) {
	module := &Module{}
	assert.Equal(t, Name, module.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name}, module.ModuleDependencies())
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(context.Background(), api))
}

func TestModuleInitializeRegistersIAMLocales(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))

	module := &Module{declarationOnly: true}
	require.NoError(t, module.Initialize(t.Context()))
	assert.ErrorContains(t, module.Initialize(t.Context()), "register iam i18n")
}

func TestDeclarationOnlyModuleRegistersAllRoutes(t *testing.T) {
	registry := authz.DefaultRegistry()
	module := NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(registry))
	assert.NoError(t, module.CheckHealth(t.Context()))

	_, api := humatest.New(t)
	require.NoError(t, module.RegisterHTTP(t.Context(), api))
	assert.Len(t, operationManifest(api.OpenAPI()), 51)
	require.NoError(t, authz.ValidateOperations(api, registry))
}

func TestDeclarationOnlyModuleRejectsRuntimeRegistrar(t *testing.T) {
	assert.Panics(t, func() { NewDeclarationOnlyModule(nil) })
	assert.Panics(t, func() { NewDeclarationOnlyModule(&authz.Registrar{}) })
	runtimeRegistrar := authz.NewRegistrar(
		authz.DefaultRegistry(), httpTokenVerifier{principalID: 1},
		httpRouteAuthorizer{platformAllowed: true}, httpMembershipChecker{},
	)
	assert.Panics(t, func() { NewDeclarationOnlyModule(runtimeRegistrar) })
}

func TestRuntimeModuleRequiresEveryDependency(t *testing.T) {
	validRuntime := &appenv.Runtime{}
	validRegistrar := authz.NewRegistrar(
		authz.DefaultRegistry(), httpTokenVerifier{principalID: 1},
		httpRouteAuthorizer{platformAllowed: true}, httpMembershipChecker{},
	)
	validChecker := httpAuthorizationEvaluator{}
	for _, build := range []func(){
		func() { NewModule(nil, validRegistrar, validChecker, Config{}) },
		func() { NewModule(validRuntime, nil, validChecker, Config{}) },
		func() { NewModule(validRuntime, &authz.Registrar{}, validChecker, Config{}) },
		func() {
			NewModule(validRuntime, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()), validChecker, Config{})
		},
		func() { NewModule(validRuntime, validRegistrar, nil, Config{}) },
	} {
		assert.Panics(t, build)
	}

	instance := NewModule(validRuntime, validRegistrar, validChecker, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve IAM id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, branchIDSource{id: 1}))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve IAM audit appender")

	providers := module.NewServices()
	require.NoError(t, instance.ProvideServices(providers))
	assert.ErrorContains(t, instance.ProvideServices(providers), "provide IAM active member checker")
}

func TestRoleGrantStoreProviderLateBindsInjectedCallbacks(t *testing.T) {
	instance := &Module{}
	store := instance.roleGrantStore()
	ctx := t.Context()
	now := time.Now()

	assert.ErrorIs(t, store.Grant(ctx, nil, 1, 2, 3, 4, 5, now, now.Add(time.Hour)), ErrUnavailable)
	for _, remove := range []func() (bool, error){
		func() (bool, error) { return store.Revoke(ctx, nil, 1, 2) },
		func() (bool, error) { return store.RemovePermanent(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemoveGroupMembership(ctx, nil, 1, 2, 3) },
		func() (bool, error) { return store.RemovePositionMembership(ctx, nil, 1, 2, 3) },
		func() (bool, error) { return store.RemoveEntityRoleBinding(ctx, nil, 1, 2, 3, 4) },
	} {
		changed, err := remove()
		assert.False(t, changed)
		assert.ErrorIs(t, err, ErrUnavailable)
	}

	grantCalls := 0
	removeCalls := 0
	instance.grants = RoleGrantStore{
		Grant: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, time.Time, time.Time) error {
			grantCalls++
			return nil
		},
		Revoke: func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
			removeCalls++
			return true, nil
		},
		RemovePermanent: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
			removeCalls++
			return true, nil
		},
		RemoveGroupMembership: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
			removeCalls++
			return true, nil
		},
		RemovePositionMembership: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
			removeCalls++
			return true, nil
		},
		RemoveEntityRoleBinding: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
			removeCalls++
			return true, nil
		},
	}

	require.NoError(t, store.Grant(ctx, nil, 1, 2, 3, 4, 5, now, now.Add(time.Hour)))
	for _, remove := range []func() (bool, error){
		func() (bool, error) { return store.Revoke(ctx, nil, 1, 2) },
		func() (bool, error) { return store.RemovePermanent(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemoveGroupMembership(ctx, nil, 1, 2, 3) },
		func() (bool, error) { return store.RemovePositionMembership(ctx, nil, 1, 2, 3) },
		func() (bool, error) { return store.RemoveEntityRoleBinding(ctx, nil, 1, 2, 3, 4) },
	} {
		changed, err := remove()
		require.NoError(t, err)
		assert.True(t, changed)
	}
	assert.Equal(t, 1, grantCalls)
	assert.Equal(t, 5, removeCalls)
}

func TestModuleDefensiveLifecycleBranches(t *testing.T) {
	var module *Module
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(t.Context(), api))

	module = NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	require.NoError(t, module.RegisterHTTP(t.Context(), api))
	assert.Len(t, operationManifest(api.OpenAPI()), 51)
}

func TestModuleMigrationSourceRemainsSelfDescribing(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_iam", source.TableName)
	assert.NotNil(t, source.FS)
	assert.Empty(t, source.Prefix)
	assert.Equal(t, int64(11), (&Module{}).MigrationMilestones()[0].Version)
}
