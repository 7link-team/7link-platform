package delivery

import (
	"context"
	"testing"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type projectChecker struct{ core.AuthorizationEvaluator }

func moduleProviders(t *testing.T) []func(*module.Services) error {
	t.Helper()
	ids, err := guid.New(1)
	require.NoError(t, err)
	eventModule := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	return []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[core.AccessTableNames](s, core.PublishedAccessTableNames("test_"))
		},
		func(s *module.Services) error {
			return module.Provide[core.TenantConstraint](s, core.TenantConstraint(func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[core.EntityOwnerResolver](s, core.EntityOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{}, false, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[idempotency.Store](s, idempotency.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"))))
		},
		func(s *module.Services) error {
			return module.Provide[governance.ProjectCreationAuthority](s, governance.ProjectCreationAuthority(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{}, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[intake.IntakeReader](s, intake.IntakeReader{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
				return intake.IntakeRef{}, false, nil
			}, ContactsOn: func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
				return nil, nil
			}})
		},
		func(s *module.Services) error {
			return module.Provide[fleet.AgentDirectory](s, fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}})
		},
		func(s *module.Services) error {
			return module.Provide[core.ActiveMemberChecker](s, memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }))
		},
		func(s *module.Services) error {
			return module.Provide[identity.PrincipalStateStore](s, identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}})
		},
		func(s *module.Services) error { return module.Provide[guid.Source](s, ids) },
		func(s *module.Services) error {
			return module.Provide[governance.SeededRoles](s, governance.SeededRoles{EnsureTenant: func(context.Context, guid.ID) error { return nil }, RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 1, nil }})
		},
		func(s *module.Services) error {
			return module.Provide[*authz.Registrar](s, authz.NewUnboundRegistrar(authz.DefaultRegistry()))
		},
		func(s *module.Services) error {
			return module.Provide[core.AuthorizationEvaluator](s, projectChecker{})
		},
		func(s *module.Services) error {
			return module.Provide[audit.Appender](s, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
		},
		func(s *module.Services) error { return module.Provide[events.Publisher](s, eventModule) },
		func(s *module.Services) error { return module.Provide[events.Catalog](s, eventModule) },
	}
}
func TestModuleDeclaresProjectOwnershipPermissionsAndOptionalProvisioning(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(t.Context()))
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{Config: module.Config{Enabled: true}})
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	require.Equal(t, Name, m.ModuleName())
	require.ElementsMatch(t, []string{platformi18n.Name, guid.Name, idempotency.Name, core.Name, identity.Name, fleet.Name, intake.Name, audit.Name, events.Name, governance.Name}, m.ModuleDependencies())
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	for _, provide := range moduleProviders(t) {
		require.NoError(t, provide(services))
	}
	require.NoError(t, services.Seal())
	reservations, err := module.Resolve[core.EntityTypeReservations](services)
	require.NoError(t, err)
	first := reservations()
	require.Equal(t, []string{"project"}, first)
	first[0] = "client"
	require.Equal(t, []string{"project"}, reservations())
	connector, err := module.Resolve[ProjectProvisioningConnector](services)
	require.NoError(t, err)
	require.NoError(t, connector.Bind(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}))
	require.ErrorIs(t, connector.Bind(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), ErrInvalidArgument)
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, m.Initialize(t.Context()), "missing optional providers must not prevent initialization")
	require.ErrorIs(t, connector.Bind(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), ErrInvalidArgument)
	for _, code := range []string{"project_read", "project_manage", "project_stage_change", "project_mode_change"} {
		action, ok := m.registrar.Registry().Find(code)
		require.True(t, ok, code)
		require.True(t, action.DataScoped)
	}
	_, exists := m.registrar.Registry().Find("project_create")
	require.False(t, exists, "pseudo-action must not become an IAM permission")
	catalog, err := module.Resolve[events.Catalog](services)
	require.NoError(t, err)
	require.Error(t, catalog.AddTypes(Name, events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}), "event must already be declared")
	require.Error(t, m.InjectServices(services), "duplicate declarations fail rather than silently replacing ownership")
}
func TestModuleRejectsMissingRequiredServices(t *testing.T) {
	for missing := range moduleProviders(t) {
		m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
		services := module.NewServices()
		for index, provide := range moduleProviders(t) {
			if index != missing {
				require.NoError(t, provide(services))
			}
		}
		require.NoError(t, services.Seal())
		require.Error(t, m.InjectServices(services))
	}
	require.ErrorIs(t, (&Module{}).InjectServices(module.NewServices()), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).Initialize(t.Context()), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).bindProvisioning(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), ErrUnavailable)
}
