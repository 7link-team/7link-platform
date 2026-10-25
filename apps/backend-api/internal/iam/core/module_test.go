package core

import (
	"context"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	infraaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
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
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, infraaudit.Name, jobs.Name, events.Name, idempotency.Name}, module.ModuleDependencies())
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(context.Background(), api))
}

func TestModuleInitializeRegistersIAMLocales(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))

	module := &Module{
		service: &Service{}, ids: branchIDSource{id: 1},
		appender: func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	}
	require.NoError(t, module.Initialize(t.Context()))
	assert.ErrorContains(t, module.Initialize(t.Context()), "register iam i18n")
}

func TestRuntimeModuleRegistersAllRoutes(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	assert.Len(t, operationManifest(harness.api.OpenAPI()), 120)
	require.NoError(t, authz.ValidateOperations(harness.api, harness.module.registrar.Registry()))
}

func TestRuntimeModuleRequiresEveryDependency(t *testing.T) {
	validRuntime := &appenv.Runtime{}
	assert.Panics(t, func() { NewModule(nil, Config{}) })

	instance := NewModule(validRuntime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve IAM id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, branchIDSource{id: 1}))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve IAM audit appender")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, branchIDSource{id: 1}))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authentication verifier")

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
		func() (bool, error) { return store.RoleGranted(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemovePermanent(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemoveGroupMembership(ctx, nil, 1, 2, 3) },
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
		RoleGranted: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
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

		RemoveEntityRoleBinding: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
			removeCalls++
			return true, nil
		},
	}

	require.NoError(t, store.Grant(ctx, nil, 1, 2, 3, 4, 5, now, now.Add(time.Hour)))
	for _, remove := range []func() (bool, error){
		func() (bool, error) { return store.Revoke(ctx, nil, 1, 2) },
		func() (bool, error) { return store.RoleGranted(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemovePermanent(ctx, nil, 1, 2, 3, now) },
		func() (bool, error) { return store.RemoveGroupMembership(ctx, nil, 1, 2, 3) },
		func() (bool, error) { return store.RemoveEntityRoleBinding(ctx, nil, 1, 2, 3, 4) },
	} {
		changed, err := remove()
		require.NoError(t, err)
		assert.True(t, changed)
	}
	assert.Equal(t, 1, grantCalls)
	assert.Equal(t, 5, removeCalls)
}

func TestModulePublishesTheSameLiveAuthorizationEvaluator(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Self reader", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "user_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID, nil)
	require.NoError(t, err)
	_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantID, role.ID, DataScopeSelf, nil)
	require.NoError(t, err)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Entity"})
	require.NoError(t, err)

	instance := NewModule(harness.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, harness.repository.ids))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, module.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: principalID}))
	require.NoError(t, services.Seal())
	evaluator, err := module.Resolve[AuthorizationEvaluator](services)
	require.NoError(t, err)
	seeder, err := module.Resolve[RoleSeeder](services)
	require.NoError(t, err)
	require.NotNil(t, seeder.EnsureRoleOn)
	require.NotNil(t, seeder.GrantPermissionOn)
	require.NotNil(t, seeder.GrantConditionalPermissionOn)
	require.NotNil(t, seeder.SeedAdministratorMembersOn)
	_, err = seeder.EnsureRoleOn(t.Context(), nil, tenantID, "lead", "Lead", "")
	require.ErrorIs(t, err, ErrUnavailable)
	resolveEntityOwner, err := module.Resolve[EntityOwnerResolver](services)
	require.NoError(t, err)
	_, _, err = resolveEntityOwner(t.Context(), entity.ID)
	require.ErrorIs(t, err, ErrUnavailable, "an owner resolver published before injection must fail closed")
	_, err = evaluator.Constraint(t.Context(), tenantID, "user_view", principalID)
	require.ErrorIs(t, err, ErrUnavailable, "unwired evaluation must fail closed")
	_, err = evaluator.EntityConstraint(t.Context(), tenantID, entity.ID, "user_view", principalID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, instance.InjectServices(services))
	// The owners of the tables core joins publish them after core's own
	// injection, so a harness that runs core alone supplies them here.
	bindModuleTables(t, instance, "sl_")
	require.Same(t, evaluator, instance.checker)
	require.Same(t, evaluator, instance.service.checker, "HTTP and business consumers must share one evaluator")
	owner, found, err := resolveEntityOwner(t.Context(), entity.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, authz.ResourceOwner{TenantID: tenantID, EntityID: entity.ID}, owner)
	constraint, err := evaluator.Constraint(t.Context(), tenantID, "user_view", principalID)
	require.NoError(t, err)
	assert.False(t, constraint.AllowAll)
	assert.Equal(t, []string{principalID.String()}, constraint.OwnerIDs)
	_, api := humatest.New(t)
	type output struct{ Body authz.DataConstraint }
	handlerCalls := 0
	handler := func(ctx context.Context, _ *struct{}) (*output, error) {
		handlerCalls++
		claims, ok := authn.FromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, tenantID, claims.TenantID)
		assert.Equal(t, entity.ID, claims.EntityID)
		assert.Equal(t, principalID, claims.PrincipalID)
		filtered, evaluateErr := evaluator.EntityConstraint(ctx, claims.TenantID, claims.EntityID, "user_view", claims.PrincipalID)
		return &output{Body: filtered}, evaluateErr
	}
	authz.RegisterEntity(instance.registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "filtered", Method: http.MethodGet, Path: "/filtered"}, authz.Guard{Resource: "user", Verb: "view", FilterData: true}, handler)
	authz.RegisterEntity(instance.registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "whole", Method: http.MethodGet, Path: "/whole"}, authz.Guard{Resource: "user", Verb: "view"}, handler)
	base := "/tenants/" + tenantID.String() + "/entities/" + entity.ID.String()
	response := api.Get(base + "/filtered")
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"owner_ids":["11"]`)
	assert.Equal(t, http.StatusForbidden, api.Get(base+"/whole").Code)
	assert.Equal(t, http.StatusForbidden, api.Get("/tenants/2/entities/"+entity.ID.String()+"/filtered").Code)
	assert.Equal(t, http.StatusForbidden, api.Get("/tenants/1/entities/999/filtered").Code)
	assert.Equal(t, 1, handlerCalls)
	_, err = harness.service.RevokePermission(t.Context(), tenantID, role.ID, "user_view")
	require.NoError(t, err)
	constraint, err = evaluator.Constraint(t.Context(), tenantID, "user_view", principalID)
	require.NoError(t, err)
	assert.False(t, constraint.AllowAll)
	assert.Empty(t, constraint.OwnerIDs, "a consumer must observe revocation without being rewired")
	assert.Equal(t, http.StatusForbidden, api.Get(base+"/filtered").Code)
	assert.Equal(t, 1, handlerCalls, "revoked requests never enter the handler")
}

// TestModulePublishesThePlatformAccessGuard proves the capability core hands the
// module that removes platform grants is the live one: it rejects a call it
// cannot honor instead of passing it to a statement, and it reads the tables it
// names under core's own deployment prefix.
func TestModulePublishesThePlatformAccessGuard(t *testing.T) {
	harness := newCoreServiceHarness(t)
	instance := NewModule(harness.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	guard, err := module.Resolve[PrincipalPlatformAccessGuard](services)
	require.NoError(t, err)

	assert.ErrorIs(t, guard(t.Context(), nil, 1), ErrInvalidArgument, "a call without a transaction must not reach a statement")
	assert.ErrorIs(t, guard(t.Context(), harness.db, 0), ErrInvalidArgument)
	assert.NoError(t, guard(t.Context(), harness.db, 11), "a principal holding no platform grant passes")
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_platform_grants (principal_id, permission_code, created_at) VALUES (11, 'zz_administer', ?)",
		harness.repository.now(),
	).Error)
	assert.ErrorIs(t, guard(t.Context(), harness.db, 11), ErrPrincipalHasPlatformAccess)
}

func TestModuleReportsDuplicateAuthorizationEvaluator(t *testing.T) {
	instance := NewModule(&appenv.Runtime{}, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, module.Provide[AuthorizationEvaluator](services, instance.checker))
	err := instance.ProvideServices(services)
	assert.ErrorIs(t, err, module.ErrServiceDuplicate)
	assert.ErrorContains(t, err, "provide IAM authorization evaluator")
}

func TestModuleDefensiveLifecycleBranches(t *testing.T) {
	var module *Module
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.Error(t, module.RegisterHTTP(t.Context(), api))
}

func TestModuleMigrationSourceRemainsSelfDescribing(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_iam", source.TableName)
	assert.NotNil(t, source.FS)
	assert.Empty(t, source.Prefix)
	assert.Equal(t, []module.MigrationMilestone{
		{Name: "base", Version: 11},
		{Name: "field_policies", Version: 12},
		{Name: "principal-erasure", Version: 13, Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "menu-translations", Version: 14},
		{Name: "default-menu-translations", Version: 15},
		{Name: "resource-menu-routes", Version: 16},
		{Name: "retire-positions", Version: 17, Dependencies: []module.MigrationMilestoneRef{{Module: "organization", Name: "retire-positions"}}},
		{Name: "role-member-expiry", Version: 18},
		{Name: "membership-periods", Version: 19},
		{Name: "menu-contributions", Version: 20},
	}, (&Module{}).MigrationMilestones())
}

func TestModulePrincipalLockConnectorBindsTheIdentityLock(t *testing.T) {
	harness := newCoreServiceHarness(t)
	instance := NewModule(harness.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[PrincipalLockConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrUnavailable, "a missing lock is refused rather than silently disabling serialization")
	require.Nil(t, instance.lockPrincipal)
	var locked guid.ID
	require.NoError(t, connector.Bind(func(_ context.Context, _ *gorm.DB, id guid.ID) error {
		locked = id
		return nil
	}))
	require.NotNil(t, instance.lockPrincipal)
	require.NoError(t, instance.lockPrincipal(t.Context(), harness.db, 42))
	require.Equal(t, guid.ID(42), locked, "the module serializes principals through the bound identity lock")

	permissionOn, err := module.Resolve[PlatformPermissionOn](services)
	require.NoError(t, err)
	_, err = permissionOn(t.Context(), harness.db, "system_menu_view", 11)
	require.ErrorIs(t, err, ErrUnavailable, "system authority is unavailable before the service is wired")
}

// duplicateCapability re-provides one capability the module publishes, so the
// module's own Provide of it collides.
func duplicateCapability[T any](t *testing.T, published *module.Services) *module.Services {
	t.Helper()
	value, err := module.Resolve[T](published)
	require.NoError(t, err)
	duplicate := module.NewServices()
	require.NoError(t, module.Provide[T](duplicate, value))
	return duplicate
}

func TestModuleReportsEveryDuplicateCapabilityByName(t *testing.T) {
	harness := newCoreServiceHarness(t)
	instance := NewModule(harness.runtime, enabledModuleConfig())
	published := module.NewServices()
	require.NoError(t, instance.ProvideServices(published))
	require.NoError(t, published.Seal())
	for message, duplicate := range map[string]*module.Services{
		"provide IAM active member checker":                    duplicateCapability[ActiveMemberChecker](t, published),
		"provide IAM principal lock connector":                 duplicateCapability[PrincipalLockConnector](t, published),
		"provide IAM administrator guard":                      duplicateCapability[AdministratorGuard](t, published),
		"provide IAM role grant store":                         duplicateCapability[RoleGrantStore](t, published),
		"provide IAM platform administrator store":             duplicateCapability[PlatformAdministratorStore](t, published),
		"provide IAM tenant bootstrapper":                      duplicateCapability[TenantBootstrapper](t, published),
		"provide authorization registrar":                      duplicateCapability[*authz.Registrar](t, published),
		"provide IAM transaction system authorization":         duplicateCapability[PlatformPermissionOn](t, published),
		"provide IAM entity owner resolver":                    duplicateCapability[EntityOwnerResolver](t, published),
		"provide IAM transaction tenant authorization":         duplicateCapability[TenantConstraintOn](t, published),
		"provide IAM authorization explanation":                duplicateCapability[AuthorizationExplanation](t, published),
		"provide IAM member department reader":                 duplicateCapability[MemberDepartmentReader](t, published),
		"provide IAM member tenant resolver":                   duplicateCapability[MemberTenantResolver](t, published),
		"provide IAM directory authorization source connector": duplicateCapability[DirectoryAuthorizationSourceConnector](t, published),
		"provide IAM directory table names connector":          duplicateCapability[DirectoryTableNamesConnector](t, published),
		"provide IAM principal table names connector":          duplicateCapability[PrincipalTableNamesConnector](t, published),
		"provide IAM access table names":                       duplicateCapability[AccessTableNames](t, published),
	} {
		err := instance.ProvideServices(duplicate)
		require.ErrorIs(t, err, module.ErrServiceDuplicate, message)
		require.ErrorContains(t, err, message)
	}
	duplicate := duplicateCapability[PrincipalPlatformAccessGuard](t, published)
	require.ErrorIs(t, instance.ProvideServices(duplicate), module.ErrServiceDuplicate)
}

func TestModuleInjectsReservedEntityTypes(t *testing.T) {
	for _, kinds := range [][]string{{"project"}, {"bad type"}} {
		t.Run(kinds[0], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			instance := NewModule(h.runtime, enabledModuleConfig())
			services := module.NewServices()
			require.NoError(t, instance.ProvideServices(services))
			require.NoError(t, module.Provide[guid.Source](services, h.repository.ids))
			require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
			require.NoError(t, module.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: 11}))
			require.NoError(t, module.Provide[EntityTypeReservations](services, func() []string { return kinds }))
			require.NoError(t, services.Seal())
			err := instance.InjectServices(services)
			if kinds[0] == "bad type" {
				require.ErrorIs(t, err, ErrInvalidArgument)
				return
			}
			require.NoError(t, err)
			kinds[0] = "client"
			require.True(t, instance.service.entityTypeReserved("project"), "declarations must be snapshotted")
			require.False(t, instance.service.entityTypeReserved("client"))
		})
	}
}
