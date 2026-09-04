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

func TestOrganizationModuleConstructionAndDeclarationOnly(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"))
	registrar := organizationTestRegistrar(&organizationMemberChecker{})
	assert.Panics(t, func() { NewModule(nil, registrar) })
	assert.Panics(t, func() { NewModule(runtime, nil) })
	assert.Panics(t, func() { NewModule(runtime, authz.NewAuthenticationOnlyRegistrar(organizationTokenVerifier{})) })
	assert.Panics(t, func() { NewModule(runtime, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry())) })
	assert.Panics(t, func() { NewDeclarationOnlyModule(nil) })
	assert.Panics(t, func() { NewDeclarationOnlyModule(registrar) })

	declaration := NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	assert.Equal(t, Name, declaration.ModuleName())
	require.NoError(t, declaration.CheckHealth(t.Context()))
	respx.Install()
	_, api := humatest.New(t)
	require.NoError(t, declaration.RegisterHTTP(t.Context(), api))
	assert.Len(t, organizationOperationManifest(api.OpenAPI()), 32)
}

func TestOrganizationModuleResolvesServicesAndProvidesGroupProvisioner(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	members := &organizationMemberChecker{prefix: h.prefix}
	guard := &organizationAdministratorGuard{}
	credentials := &invitationCredentialFixture{}
	creator := identity.InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 900, nil
	})
	instance := NewModule(h.runtime, organizationTestRegistrar(members))
	assert.Equal(t, []string{
		platformi18n.Name, guid.Name, audit.Name, core.Name, authn.Name, identity.Name,
	}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "services are unavailable")

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
			if err := module.Provide[core.ActiveMemberChecker](services, members); err != nil {
				return err
			}
			return module.Provide[core.AdministratorGuard](services, guard)
		}},
		&organizationDependencyProvider{name: authn.Name, provide: func(services *module.Services) error {
			return module.Provide[authn.InvitationCredentials](services, credentials)
		}},
		&organizationDependencyProvider{name: identity.Name, provide: func(services *module.Services) error {
			return module.Provide[identity.InvitationPrincipalCreator](services, creator)
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
	assert.Len(t, organizationOperationManifest(api.OpenAPI()), 32)
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
	}
	expected := []string{
		"active member checker", "administrator guard", "invitation credentials",
		"invitation principal creator", "id generator", "audit appender",
	}
	for count, message := range expected {
		services := module.NewServices()
		for _, provide := range providers[:count] {
			require.NoError(t, provide(services))
		}
		require.NoError(t, services.Seal())
		instance := NewModule(runtime, organizationTestRegistrar(members))
		assert.ErrorContains(t, instance.InjectServices(services), message)
	}
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

	declaration := NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	services := module.NewServices()
	require.NoError(t, declaration.ProvideServices(services))
	assert.Error(t, declaration.ProvideServices(services))
}
