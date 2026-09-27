package organization

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
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

type organizationTokenVerifier struct{}

func (organizationTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{Subject: "1", PrincipalID: 1, SubjectType: authnext.SubjectTypePrincipal}, nil
}

type organizationPermissionChecker struct{}

func (organizationPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (organizationPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

func (organizationPermissionChecker) CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

type organizationDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (provider *organizationDependencyProvider) ModuleName() string { return provider.name }

func (provider *organizationDependencyProvider) ProvideServices(services *module.Services) error {
	return provider.provide(services)
}

func organizationTestRegistrar(members authz.MembershipChecker) *authz.Registrar {
	return authz.NewRegistrar(
		authz.DefaultRegistry(), organizationTokenVerifier{}, organizationPermissionChecker{}, members,
	)
}

func TestOrganizationModuleConstructionAndReadiness(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"))
	assert.Panics(t, func() { NewModule(nil, Config{}) })
	instance := NewModule(runtime, Config{})
	assert.Equal(t, Name, instance.ModuleName())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
}

func TestOrganizationModuleResolvesServicesAndProvidesGroupProvisioner(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	members := &organizationMemberChecker{prefix: h.prefix}
	guard := &organizationAdministratorGuard{}
	credentials := &invitationCredentialFixture{}
	creator := identity.InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 900, nil
	})
	instance := NewModule(h.runtime, enabledModuleConfig())
	assert.Equal(t, []string{
		platformi18n.Name, guid.Name, audit.Name, core.Name, authn.Name, identity.Name,
	}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "module is unavailable")

	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&organizationDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newOrganizationTestIDs(9000))
		}},
		&organizationDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			return module.Provide[audit.Appender](services, h.appender)
		}},
		&organizationDependencyProvider{name: core.Name, provide: func(services *module.Services) error {
			if err := module.Provide[*authz.Registrar](services, organizationTestRegistrar(members)); err != nil {
				return err
			}
			if err := module.Provide[core.ActiveMemberChecker](services, members); err != nil {
				return err
			}
			if err := module.Provide[core.AdministratorGuard](services, guard); err != nil {
				return err
			}
			if err := module.Provide[core.TenantBootstrapper](services, newTenantBootstrapStub().capability()); err != nil {
				return err
			}
			if err := module.Provide[core.DirectoryAuthorizationSourceConnector](services, core.DirectoryAuthorizationSourceConnector{
				Bind: func(core.DirectoryAuthorizationSourceStore) error { return nil },
			}); err != nil {
				return err
			}
			if err := module.Provide[core.DirectoryTableNamesConnector](services, core.DirectoryTableNamesConnector{
				Bind: func(core.DirectoryTableNames) error { return nil },
			}); err != nil {
				return err
			}
			return module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(h.runtime.TablePrefix()))
		}},
		&organizationDependencyProvider{name: authn.Name, provide: func(services *module.Services) error {
			if err := module.Provide[authn.InvitationCredentials](services, credentials); err != nil {
				return err
			}
			// The consumers publish these during Wire in a running deployment;
			// this registry fakes them so organization's own wiring resolves.
			return module.Provide[authn.DirectoryTableNamesConnector](services, authn.DirectoryTableNamesConnector{
				Bind: func(authn.DirectoryTableNames) error { return nil },
			})
		}},
		&organizationDependencyProvider{name: identity.Name, provide: func(services *module.Services) error {
			if err := module.Provide[identity.InvitationPrincipalCreator](services, creator); err != nil {
				return err
			}
			if err := module.Provide[identity.TenantOwnerLookup](services, newTenantOwnerStub().capability()); err != nil {
				return err
			}
			return module.Provide[identity.DirectoryTableNamesConnector](services, identity.DirectoryTableNamesConnector{
				Bind: func(identity.DirectoryTableNames) error { return nil },
			})
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	provisioner, err := module.Resolve[GroupProvisioner](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, provisioner)
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	require.NotNil(t, instance.Platforms())
	assert.NotSame(t, instance.tenants, instance.Platforms().TenantService)
	assert.Equal(t, ScopePlatform, instance.Platforms().kind())
	created, err := instance.CreateProvisionedTo(t.Context(), h.db, 1, ProvisionedGroupInput{DisplayName: "Provisioned", Active: true})
	require.NoError(t, err)
	stored, err := instance.Get(t.Context(), 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, stored.ID)
	membersList, err := instance.ListMembers(t.Context(), 1, created.ID)
	require.NoError(t, err)
	assert.Empty(t, membersList)
	replaced, err := instance.ReplaceProvisionedTo(t.Context(), h.db, 1, created.ID, ProvisionedGroupInput{DisplayName: "Renamed", Active: true})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", replaced.Name)
	disabled, err := instance.DisableProvisionedTo(t.Context(), h.db, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, disabled.Status)
	_, api = humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, organizationOperationManifest(api.OpenAPI()), 34)
}

func TestOrganizationModuleReportsMissingWiring(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"))
	members := &organizationMemberChecker{}
	guard := &organizationAdministratorGuard{}
	credentials := &invitationCredentialFixture{}
	creator := identity.InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 1, nil
	})
	providers := []func(*module.Services) error{
		func(services *module.Services) error {
			return module.Provide[*authz.Registrar](services, organizationTestRegistrar(members))
		},

		func(services *module.Services) error {
			return module.Provide[core.ActiveMemberChecker](services, members)
		},
		func(services *module.Services) error { return module.Provide[core.AdministratorGuard](services, guard) },
		func(services *module.Services) error {
			return module.Provide[authn.InvitationCredentials](services, credentials)
		},
		func(services *module.Services) error {
			return module.Provide[identity.InvitationPrincipalCreator](services, creator)
		},
		func(services *module.Services) error {
			return module.Provide[guid.Source](services, newOrganizationTestIDs(1))
		},
		func(services *module.Services) error {
			return module.Provide[audit.Appender](services, audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
		},
		func(services *module.Services) error {
			return module.Provide[core.TenantBootstrapper](services, newTenantBootstrapStub().capability())
		},
		func(services *module.Services) error {
			return module.Provide[identity.TenantOwnerLookup](services, newTenantOwnerStub().capability())
		},
		func(services *module.Services) error {
			return module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames("sl_"))
		},
	}
	// The connectors organization's wiring resolves are the last four, in the
	// order it resolves them, so the nil-Bind check below can replace one by
	// position without restating the whole list.
	connectors := []struct {
		message string
		provide func(*module.Services) error
	}{
		{"directory authorization source connector", func(services *module.Services) error {
			return module.Provide[core.DirectoryAuthorizationSourceConnector](services, core.DirectoryAuthorizationSourceConnector{
				Bind: func(core.DirectoryAuthorizationSourceStore) error { return nil },
			})
		}},
		{"directory table names connector", func(services *module.Services) error {
			return module.Provide[core.DirectoryTableNamesConnector](services, core.DirectoryTableNamesConnector{
				Bind: func(core.DirectoryTableNames) error { return nil },
			})
		}},
		{"authn directory table names connector", func(services *module.Services) error {
			return module.Provide[authn.DirectoryTableNamesConnector](services, authn.DirectoryTableNamesConnector{
				Bind: func(authn.DirectoryTableNames) error { return nil },
			})
		}},
		{"identity directory table names connector", func(services *module.Services) error {
			return module.Provide[identity.DirectoryTableNamesConnector](services, identity.DirectoryTableNamesConnector{
				Bind: func(identity.DirectoryTableNames) error { return nil },
			})
		}},
	}
	for _, connector := range connectors {
		providers = append(providers, connector.provide)
	}
	expected := []string{
		"authorization registrar", "active member checker", "administrator guard", "invitation credentials",
		"invitation principal creator", "id generator", "audit appender", "tenant bootstrapper",
		"tenant owner lookup", "access tables", "directory authorization source connector", "directory table names connector",
		"authn directory table names connector", "identity directory table names connector",
	}
	require.Len(t, expected, len(providers), "every provider is the one the next message reports missing")
	for count, message := range expected {
		services := module.NewServices()
		for _, provide := range providers[:count] {
			require.NoError(t, provide(services))
		}
		require.NoError(t, services.Seal())
		instance := NewModule(runtime, enabledModuleConfig())
		assert.ErrorContains(t, instance.InjectServices(services), message)
	}

	// A consumer that provides a connector without a bind would leave the tables
	// unpublished while module wiring reported success, so wiring refuses it.
	// Every consumer module is checked, because a single unchecked one silently
	// reintroduces the empty table name the connectors exist to remove.
	for index, connector := range connectors {
		t.Run(connector.message, func(t *testing.T) {
			services := module.NewServices()
			for providerIndex, provide := range providers {
				if providerIndex == len(providers)-len(connectors)+index {
					require.NoError(t, provideUnboundConnector(connector.message, services))
					continue
				}
				require.NoError(t, provide(services))
			}
			require.NoError(t, services.Seal())
			instance := NewModule(runtime, enabledModuleConfig())
			assert.ErrorContains(t, instance.InjectServices(services), connector.message+": bind is required")
		})
	}
}

// provideUnboundConnector provides one of organization's consumer connectors
// with no Bind function, which is what a consumer that declared the type and
// never filled it in hands its owner.
func provideUnboundConnector(message string, services *module.Services) error {
	switch message {
	case "directory authorization source connector":
		return module.Provide[core.DirectoryAuthorizationSourceConnector](services, core.DirectoryAuthorizationSourceConnector{})
	case "directory table names connector":
		return module.Provide[core.DirectoryTableNamesConnector](services, core.DirectoryTableNamesConnector{})
	case "authn directory table names connector":
		return module.Provide[authn.DirectoryTableNamesConnector](services, authn.DirectoryTableNamesConnector{})
	default:
		return module.Provide[identity.DirectoryTableNamesConnector](services, identity.DirectoryTableNamesConnector{})
	}
}

// TestOrganizationModuleRefusesToStartWithoutThePublishedTableNames is what the
// per-statement checks collapsed into. Organization holds the tables core owns
// as typed fields now, so a name that never arrived can no longer be reported
// by the statement that needed it: it is caught once, here, and the deployment
// does not start rather than joining an empty table name.
func TestOrganizationModuleRefusesToStartWithoutThePublishedTableNames(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"))
	members := &organizationMemberChecker{}
	creator := identity.InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 1, nil
	})

	services := module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, organizationTestRegistrar(members)))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](services, members))
	require.NoError(t, module.Provide[core.AdministratorGuard](services, &organizationAdministratorGuard{}))
	require.NoError(t, module.Provide[authn.InvitationCredentials](services, &invitationCredentialFixture{}))
	require.NoError(t, module.Provide[identity.InvitationPrincipalCreator](services, creator))
	require.NoError(t, module.Provide[guid.Source](services, newOrganizationTestIDs(1)))
	require.NoError(t, module.Provide[audit.Appender](services,
		audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })))
	require.NoError(t, module.Provide[core.TenantBootstrapper](services, newTenantBootstrapStub().capability()))
	require.NoError(t, module.Provide[identity.TenantOwnerLookup](services, newTenantOwnerStub().capability()))
	require.NoError(t, module.Provide[core.AccessTableNames](services, core.AccessTableNames{}))
	require.NoError(t, module.Provide[core.DirectoryAuthorizationSourceConnector](services, core.DirectoryAuthorizationSourceConnector{
		Bind: func(core.DirectoryAuthorizationSourceStore) error { return nil },
	}))
	require.NoError(t, module.Provide[core.DirectoryTableNamesConnector](services, core.DirectoryTableNamesConnector{
		Bind: func(core.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, module.Provide[authn.DirectoryTableNamesConnector](services, authn.DirectoryTableNamesConnector{
		Bind: func(authn.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, module.Provide[identity.DirectoryTableNamesConnector](services, identity.DirectoryTableNamesConnector{
		Bind: func(identity.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, services.Seal())

	instance := NewModule(runtime, enabledModuleConfig())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	assert.ErrorContains(t, instance.Initialize(t.Context()),
		"the core module must publish the table names organization joins")
}

func TestOrganizationModuleProvisionerUnavailableBeforeInitialize(t *testing.T) {
	var instance *Module
	_, err := instance.Get(t.Context(), 1, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.ListMembers(t.Context(), 1, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.CreateProvisionedTo(t.Context(), nil, 1, ProvisionedGroupInput{})
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.ReplaceProvisionedTo(t.Context(), nil, 1, 2, ProvisionedGroupInput{})
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = instance.DisableProvisionedTo(t.Context(), nil, 1, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Error(t, instance.RegisterHTTP(t.Context(), nil))

	declaration := NewModule(appenv.NewRuntime(appenv.NewIdentity("organization-test", "1")), enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, declaration.ProvideServices(services))
	assert.Error(t, declaration.ProvideServices(services))
}

// TestDepartmentOwnerResolverPublishesTheOwningTenantAndEntity pins the
// capability the authorization layer uses to decide whose department a
// globally addressed identifier names. Every answer it can give is load
// bearing: the owning tenant and entity authorize the request, "not found"
// keeps an identifier from another tenant from being probed, and an error must
// stay an error rather than collapse into either of the other two.
func TestDepartmentOwnerResolverPublishesTheOwningTenantAndEntity(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	created, err := h.service.Create(t.Context(), 1, CreateDepartment{
		EntityID: organizationEntityOne, Name: "Engineering",
	})
	require.NoError(t, err)

	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolve, err := module.Resolve[core.DepartmentOwnerResolver](services)
	require.NoError(t, err)

	_, found, err := resolve(t.Context(), created.ID)
	assert.ErrorIs(t, err, ErrUnavailable, "an unwired module cannot vouch for a department's owner")
	assert.False(t, found)

	instance.service = h.service
	owner, found, err := resolve(t.Context(), created.ID)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, authz.ResourceOwner{TenantID: 1, EntityID: organizationEntityOne}, owner)

	_, found, err = resolve(t.Context(), 987654)
	require.NoError(t, err)
	assert.False(t, found, "an unknown department has no owner to authorize against")

	unwired := &Repository{}
	_, _, err = unwired.resolveDepartmentOwner(t.Context(), created.ID)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_departments").Error)
	_, found, err = resolve(t.Context(), created.ID)
	assert.ErrorContains(t, err, "resolve department owner")
	assert.False(t, found, "an unreadable directory must not authorize as an unowned resource")
}
