package core

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModuleIdentityDependenciesAndReadiness(t *testing.T) {
	module := &Module{}
	assert.Equal(t, Name, module.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name}, module.ModuleDependencies())
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(context.Background(), api))
}

func TestModuleInitializeRegistersIAMLocales(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))

	module := &Module{}
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
	validAppender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	validIDs := guid.Source(branchIDSource{id: 1})

	for _, build := range []func(){
		func() { NewModule(nil, validRegistrar, validChecker, validAppender, validIDs) },
		func() { NewModule(validRuntime, nil, validChecker, validAppender, validIDs) },
		func() { NewModule(validRuntime, &authz.Registrar{}, validChecker, validAppender, validIDs) },
		func() { NewModule(validRuntime, validRegistrar, nil, validAppender, validIDs) },
		func() { NewModule(validRuntime, validRegistrar, validChecker, nil, validIDs) },
		func() { NewModule(validRuntime, validRegistrar, validChecker, validAppender, nil) },
	} {
		assert.Panics(t, build)
	}
}

func TestModuleDefensiveLifecycleBranches(t *testing.T) {
	var module *Module
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(t.Context(), api))
	assert.Panics(t, func() { module.RegisterREST(api) })

	module = NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	module.RegisterREST(api)
	assert.Len(t, operationManifest(api.OpenAPI()), 51)
}

func TestModuleMigrationSourceRemainsSelfDescribing(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_iam", source.TableName)
	assert.NotNil(t, source.FS)
	assert.Empty(t, source.Prefix)
	assert.Equal(t, int64(11), (&Module{}).MigrationMilestones()[0].Version)
}
