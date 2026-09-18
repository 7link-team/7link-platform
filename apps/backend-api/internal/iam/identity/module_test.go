package identity

import (
	"7link.com/platform/internal/files"
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
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
		identityRouteAuthorizer{userUpdateAllowed: true},
		identityMembershipChecker{},
	)
}

func TestModuleRequiresRuntimeAtConstruction(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { NewModule(nil, Config{}) })
}

func TestModuleIdentityDependenciesAndReadinessBeforeInitialize(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))

	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("identity-test", "1")),
		enabledModuleConfig(),
	)
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, core.Name, files.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "service is unavailable")
	assert.ErrorContains(t, instance.Initialize(t.Context()), "id generator")
}

// identityWiredRegistry registers identity behind everything it resolves. The
// receiver stands for a module that JOINs identity's tables: identity resolves a
// type of its own and pushes its names into it, because the real consumer --
// authn -- is activated before identity. See PrincipalTableNamesReceiver.
func identityWiredRegistry(
	t *testing.T,
	harness *identityHarness,
	receiver PrincipalTableNamesReceiver,
	state ...PrincipalStateReceiver,
) (*module.Registry, *Module) {
	t.Helper()
	instance := NewModule(harness.runtime, enabledModuleConfig())
	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		files.NewModule(harness.runtime, files.Config{Config: module.Config{Enabled: true}, LocalRoot: t.TempDir()}),
		&identityDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newIdentityTestIDs(9000))
		}},
		&identityDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			if err := module.Provide[audit.Appender](services, audit.Appender(
				func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
			)); err != nil {
				return err
			}
			return module.Provide[audit.ErasureRecorder](services, audit.ErasureRecorder(
				func(context.Context, audit.ErasureObligation) error { return nil },
			))
		}},
		&identityDependencyProvider{name: core.Name, provide: func(services *module.Services) error {
			if err := module.Provide[core.AuthorizationEvaluator](services, core.NewAuthorizer(harness.runtime)); err != nil {
				return err
			}
			if err := module.Provide[*authz.Registrar](services, identityTestRegistrar()); err != nil {
				return err
			}
			if err := module.Provide[core.AdministratorGuard](services, harness.guard); err != nil {
				return err
			}
			if err := module.Provide[core.PrincipalTableNamesConnector](services, core.PrincipalTableNamesConnector{
				Bind: func(core.PrincipalTableNames) error { return nil },
			}); err != nil {
				return err
			}
			if err := module.Provide[core.AccessTableNames](services,
				core.PublishedAccessTableNames(harness.runtime.TablePrefix()),
			); err != nil {
				return err
			}
			// authn provides this in a deployment; the registry runs identity
			// alone, so it stands in for the owner of the credential tables.
			if err := module.Provide[PrincipalCredentialStore](
				services, authnCredentialFixture(harness.runtime.TablePrefix()),
			); err != nil {
				return err
			}
			if len(state) > 0 {
				if err := module.Provide[PrincipalStateReceiver](services, state[0]); err != nil {
					return err
				}
			}
			return module.Provide[PrincipalTableNamesReceiver](services, receiver)
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	return registry, instance
}

func TestModuleResolvesDependenciesAndRegistersItsContract(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	var publishedToConsumer PrincipalTableNames
	registry, instance := identityWiredRegistry(t, harness, PrincipalTableNamesReceiver{
		Bind: func(names PrincipalTableNames) error { publishedToConsumer = names; return nil },
	})

	active, exists := registry.ByName(Name)
	require.True(t, exists)
	assert.Same(t, instance, active)
	require.NoError(t, registry.Wire(nil))
	require.True(t, publishedToConsumer.Bound(), "identity must push the tables it owns into the receiver")
	assert.Equal(t, "sl_iam_principals", publishedToConsumer.Principals.String())
	assert.Equal(t, "sl_iam_service_accounts", publishedToConsumer.ServiceAccounts.String())
	// The owners publish their table names during Wire in a running deployment;
	// this registry runs identity alone, so it publishes what they would.
	bindModuleForeignTables(t, instance, harness.prefix)

	provisioner, err := module.Resolve[IdentityProvisioner](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, provisioner)
	creator, err := module.Resolve[InvitationPrincipalCreator](registry.Services())
	require.NoError(t, err)
	registrationCreator, err := module.Resolve[RegistrationPrincipalCreator](registry.Services())
	require.NoError(t, err)
	accountValidator, err := module.Resolve[ServiceAccountValidator](registry.Services())
	require.NoError(t, err)
	_, err = accountValidator(t.Context(), harness.db, 7, 1, time.Now())
	assert.ErrorIs(t, err, ErrUnavailable)

	// The invitation capability resolves the service per call, so it reports the
	// module as unavailable before Initialize instead of panicking.
	_, err = creator(t.Context(), harness.db, 7, "alice", testPassword, "Alice", "alice@example.test")
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = registrationCreator(t.Context(), harness.db, "pending@example.test", "hash", "Pending", time.Now())
	assert.ErrorIs(t, err, ErrUnavailable)

	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	harness.seedTenant(t, 7, "active")
	account, err := instance.CreateServiceAccount(t.Context(), 7, "scim-agent", "SCIM agent", "", nil)
	require.NoError(t, err)
	validAccount, err := accountValidator(t.Context(), harness.db, 7, account.ID, time.Now())
	require.NoError(t, err)
	assert.True(t, validAccount)

	invited, err := creator(t.Context(), harness.db, 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	assert.Equal(t, "alice", harness.principal(t, invited).LoginName)
	pending, err := registrationCreator(t.Context(), harness.db, "pending@example.test", "hash", "Pending", time.Now())
	require.NoError(t, err)
	assert.Equal(t, "pending@example.test", harness.principal(t, pending).LoginName)

	principal, err := provisioner.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	found, err := provisioner.Get(t.Context(), 7, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, principal.ID, found.ID)

	require.NoError(t, registry.Start(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, registry.RegisterHTTP(t.Context(), api))
	assert.Len(t, identityOperationManifest(api.OpenAPI()), 29)
	require.NoError(t, registry.Stop(context.Background()))
	require.NoError(t, registry.Deinitialize(context.Background()))
}

func TestModuleReportsMissingWiring(t *testing.T) {
	t.Parallel()

	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("identity-test", "1")),
		enabledModuleConfig(),
	)

	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve identity authorization registrar")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, identityTestRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newIdentityTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve identity audit appender")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, identityTestRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newIdentityTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, audit.Appender(
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve audit erasure recorder")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, identityTestRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newIdentityTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services, audit.Appender(
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	)))
	require.NoError(t, module.Provide[audit.ErasureRecorder](services, audit.ErasureRecorder(
		func(context.Context, audit.ErasureObligation) error { return nil },
	)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve identity administrator guard")

	providers := module.NewServices()
	require.NoError(t, instance.ProvideServices(providers))
	assert.ErrorContains(t, instance.ProvideServices(providers), "provide identity provisioner")
}

// TestIdentityModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. identity holds the names it joins as
// typed fields now, so a name that never arrived can no longer be reported by
// the statement that needed it: it is caught once, here, and the deployment
// does not start rather than joining an empty table name.
//
// core is an ordinary provider, so a core that published nothing still arrives
// as the zero struct. organization and oauth are activated after identity and
// publish through the connectors identity provides, so an owner that is not in
// the deployment is one that never called the connector it was given.
func TestIdentityModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")

	for _, absent := range []struct {
		owner                 string
		access                core.AccessTableNames
		organizationPublishes bool
		oauthPublishes        bool
	}{
		{"core", core.AccessTableNames{}, true, true},
		{"organization", core.PublishedAccessTableNames(harness.prefix), false, true},
		{"oauth", core.PublishedAccessTableNames(harness.prefix), true, false},
	} {
		t.Run(absent.owner, func(t *testing.T) {
			require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
			instance := NewModule(harness.runtime, enabledModuleConfig())

			services := module.NewServices()
			require.NoError(t, instance.ProvideServices(services))
			require.NoError(t, module.Provide[*authz.Registrar](services, identityTestRegistrar()))
			require.NoError(t, module.Provide[guid.Source](services, newIdentityTestIDs(9500)))
			require.NoError(t, module.Provide[audit.Appender](services, audit.Appender(
				func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
			)))
			require.NoError(t, module.Provide[audit.ErasureRecorder](services, audit.ErasureRecorder(
				func(context.Context, audit.ErasureObligation) error { return nil },
			)))
			require.NoError(t, module.Provide[core.AdministratorGuard](services, harness.guard))
			require.NoError(t, module.Provide[core.AuthorizationEvaluator](services, core.NewAuthorizer(harness.runtime)))
			require.NoError(t, module.Provide[core.PrincipalTableNamesConnector](services,
				core.PrincipalTableNamesConnector{Bind: func(core.PrincipalTableNames) error { return nil }}))
			require.NoError(t, module.Provide[core.AccessTableNames](services, absent.access))
			require.NoError(t, module.Provide[files.Driver](services, harness.repository.storage))
			require.NoError(t, module.Provide[*files.AccountObjects](services, harness.repository.accountFiles))
			require.NoError(t, services.Seal())

			require.NoError(t, instance.InjectServices(services))
			if absent.organizationPublishes {
				directory, err := module.Resolve[DirectoryTableNamesConnector](services)
				require.NoError(t, err)
				require.NoError(t, directory.Bind(publishedDirectoryTables(harness.prefix)))
			}
			if absent.oauthPublishes {
				oauth, err := module.Resolve[OAuthTableNamesConnector](services)
				require.NoError(t, err)
				require.NoError(t, oauth.Bind(publishedOAuthTables(harness.prefix)))
			}

			assert.ErrorContains(t, instance.Initialize(t.Context()),
				"the core, organization, and oauth modules must publish the table names identity joins")
		})
	}
}

// TestModuleRejectsAReceiverWithoutABind covers the one wiring mistake the
// inverted direction makes possible: a consumer registers the receiver but
// leaves Bind nil, which would make identity publish its names into nothing and
// leave that consumer naming identity's tables itself.
func TestModuleRejectsAReceiverWithoutABind(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	registry, _ := identityWiredRegistry(t, harness, PrincipalTableNamesReceiver{})
	assert.ErrorContains(t, registry.Wire(nil), "bind is required")
}

// TestModulePushesPrincipalStateAndReportsABrokenReceiver covers the second
// receiver: a module activated before identity gets identity's own statements
// pushed into it, and a receiver that cannot take them is a wiring mistake
// rather than something to publish into silently.
func TestModulePushesPrincipalStateAndReportsABrokenReceiver(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	noopTables := PrincipalTableNamesReceiver{Bind: func(PrincipalTableNames) error { return nil }}

	t.Run("pushes the owner's statements", func(t *testing.T) {
		var pushed PrincipalStateStore
		registry, _ := identityWiredRegistry(t, harness, noopTables, PrincipalStateReceiver{
			Bind: func(store PrincipalStateStore) error { pushed = store; return nil },
		})
		require.NoError(t, registry.Wire(nil))
		require.NotNil(t, pushed.LockActiveRow)
		_, found, err := pushed.LockActiveRow(t.Context(), harness.db, 404)
		require.NoError(t, err)
		assert.False(t, found, "the pushed capability must be the owner's working statements")
	})

	t.Run("a receiver without a bind is reported", func(t *testing.T) {
		registry, _ := identityWiredRegistry(t, harness, noopTables, PrincipalStateReceiver{})
		assert.ErrorContains(t, registry.Wire(nil), "principal state receiver: bind is required")
	})

	t.Run("a receiver that refuses is reported", func(t *testing.T) {
		registry, _ := identityWiredRegistry(t, harness, noopTables, PrincipalStateReceiver{
			Bind: func(PrincipalStateStore) error { return assert.AnError },
		})
		assert.ErrorContains(t, registry.Wire(nil), "bind identity principal state")
	})
}
