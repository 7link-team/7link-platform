package identity

import (
	"context"
	"testing"

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

type identityDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (m *identityDependencyProvider) ModuleName() string { return m.name }

func (m *identityDependencyProvider) ProvideServices(services *module.Services) error {
	return m.provide(services)
}

func identityTestRegistrar() *authz.Registrar {
	return authz.NewRegistrar(
		authz.DefaultRegistry(),
		identityTokenVerifier{principalID: 11},
		identityRouteAuthorizer{administerAllowed: true},
		identityMembershipChecker{},
	)
}

func TestModuleRequiresItsConstructionCollaborators(t *testing.T) {
	t.Parallel()

	runtime := appenv.NewRuntime(appenv.NewIdentity("identity-test", "1"))
	registrar := identityTestRegistrar()
	guard := &stubAdministratorGuard{}
	assert.Panics(t, func() { NewModule(nil, registrar, guard) })
	assert.Panics(t, func() { NewModule(runtime, nil, guard) })
	assert.Panics(t, func() { NewModule(runtime, registrar, nil) })
	assert.Panics(t, func() {
		NewModule(runtime, authz.NewAuthenticationOnlyRegistrar(identityTokenVerifier{}), guard)
	})
}

func TestModuleIdentityDependenciesAndReadinessBeforeInitialize(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))

	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("identity-test", "1")),
		identityTestRegistrar(),
		&stubAdministratorGuard{},
	)
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "service is unavailable")
	assert.ErrorContains(t, instance.Initialize(t.Context()), "id generator")
}

func TestModuleResolvesDependenciesAndRegistersItsContract(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	instance := NewModule(harness.runtime, identityTestRegistrar(), harness.guard)

	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&identityDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newIdentityTestIDs(9000))
		}},
		&identityDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			return module.Provide[audit.Appender](services, audit.Appender(
				func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
			))
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	active, exists := registry.ByName(Name)
	require.True(t, exists)
	assert.Same(t, instance, active)
	require.NoError(t, registry.Wire(nil))

	provisioner, err := module.Resolve[IdentityProvisioner](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, provisioner)
	creator, err := module.Resolve[InvitationPrincipalCreator](registry.Services())
	require.NoError(t, err)

	// The invitation capability resolves the service per call, so it reports the
	// module as unavailable before Initialize instead of panicking.
	_, err = creator(t.Context(), harness.db, 7, "alice", testPassword, "Alice", "alice@example.test")
	assert.ErrorIs(t, err, ErrUnavailable)

	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))

	invited, err := creator(t.Context(), harness.db, 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	assert.Equal(t, "alice", harness.principal(t, invited).LoginName)

	principal, err := provisioner.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	found, err := provisioner.Get(t.Context(), 7, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, principal.ID, found.ID)

	require.NoError(t, registry.Start(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, registry.RegisterHTTP(t.Context(), api))
	assert.Len(t, identityOperationManifest(api.OpenAPI()), 14)
	require.NoError(t, registry.Stop(context.Background()))
	require.NoError(t, registry.Deinitialize(context.Background()))
}

func TestModuleReportsMissingWiring(t *testing.T) {
	t.Parallel()

	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("identity-test", "1")),
		identityTestRegistrar(),
		&stubAdministratorGuard{},
	)

	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve identity id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, newIdentityTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve identity audit appender")

	providers := module.NewServices()
	require.NoError(t, instance.ProvideServices(providers))
	assert.ErrorContains(t, instance.ProvideServices(providers), "provide identity provisioner")
}
