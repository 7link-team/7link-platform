package delivery

import (
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/performance"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type projectChecker struct{ core.AuthorizationEvaluator }

// Validate the actual declarations as injection contributes them, including loops.
type deliveryActivityCatalog struct {
	t *testing.T
	events.Catalog
}

func (c deliveryActivityCatalog) AddTypes(owner string, specs ...events.TypeSpec) error {
	if err := c.Catalog.AddTypes(owner, specs...); err != nil {
		return err
	}
	for _, spec := range specs {
		base := deliveryi18n.Namespace + ".activity_event_" + strings.ReplaceAll(spec.Name, ".", "_")
		require.Equal(c.t, base+"_name", spec.DisplayNameKey, spec.Name)
		require.Equal(c.t, base+"_description", spec.DescriptionKey, spec.Name)
	}
	return nil
}

func moduleProviders(t *testing.T) []func(*module.Services) error {
	t.Helper()
	ids, err := guid.New(1)
	require.NoError(t, err)
	eventModule := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	return []func(*module.Services) error{
		consumerProviders,
		func(s *module.Services) error {
			if err := module.Provide[intake.DelegatedNewSources](s, intake.DelegatedNewSources{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.TriageSource, string, error) {
				return intake.TriageSource{}, "", nil
			}, ConvertOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID) error {
				return nil
			}}); err != nil {
				return err
			}
			return module.Provide[intake.DelegatedNewConnector](s, intake.DelegatedNewConnector{Bind: func(check intake.DelegatedNewAuthority) error { require.NotNil(t, check); return nil }})
		},

		func(s *module.Services) error {
			return module.Provide[governance.InboxSources](s, inboxTestRegistry{})
		},
		func(s *module.Services) error {
			return module.Provide[collab.MessageWorkConnector](s, collab.MessageWorkConnector{Bind: func(collab.MessageWorkCreator, collab.MessageWorkReplay) error { return nil }})
		},
		func(s *module.Services) error {
			return files.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), files.Config{LocalRoot: t.TempDir()}).ProvideServices(s)
		},
		func(s *module.Services) error {
			return module.Provide[governance.DeliveryAuthority](s, governance.DeliveryAuthority(func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{}, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[authnext.ReauthenticationConsumer](s, authnext.ReauthenticationConsumer(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }))
		},

		func(s *module.Services) error {
			if err := module.Provide[dlock.TryRenewingLease](s, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
				return nil, dlock.ErrDatabaseUnavailable
			}); err != nil {
				return err
			}
			return module.Provide[dlock.TransactionGuard](s, dlock.TransactionGuard(func(context.Context, *gorm.DB, string) error { return nil }))
		},
		func(s *module.Services) error {
			return module.Provide[governance.ProjectStageAuthority](s, governance.ProjectStageAuthority(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{}, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[core.EntityConstraintOn](s, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}))
		},
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
			return module.Provide[fleet.SpawnGrantDirectory](s, fleet.SpawnGrantDirectory{AgentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}})
		},
		func(s *module.Services) error {
			return module.Provide[core.ActiveMemberChecker](s, memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }))
		},
		func(s *module.Services) error {
			return module.Provide[core.TenantConstraintOn](s, core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}))
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
		func(s *module.Services) error {
			return module.Provide[events.Catalog](s, deliveryActivityCatalog{t: t, Catalog: eventModule})
		},
		func(s *module.Services) error { return module.Provide[events.SubscriberCatalog](s, eventModule) },
		func(s *module.Services) error { return module.Provide[jobs.Queue](s, &autonomyQueueProbe{}) },
	}
}
func TestModuleDeclaresProjectOwnershipPermissionsAndOptionalProvisioning(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(t.Context()))
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{Config: module.Config{Enabled: true}})
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	require.Equal(t, Name, m.ModuleName())
	require.ElementsMatch(t, []string{performance.Name, knowledge.Name, platformi18n.Name, guid.Name, idempotency.Name, core.Name, identity.Name, fleet.Name, intake.Name, audit.Name, events.Name, governance.Name, jobs.Name, dlock.Name, files.Name, iamauthn.Name}, m.ModuleDependencies())
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
	for _, code := range []string{"project_read", "project_manage", "project_stage_change", "project_mode_change", "work_item_read", "work_item_write"} {
		action, ok := m.registrar.Registry().Find(code)
		require.True(t, ok, code)
		require.True(t, action.DataScoped)
	}
	_, exists := m.registrar.Registry().Find("project_create")
	require.False(t, exists, "pseudo-action must not become an IAM permission")
	catalog, err := module.Resolve[events.Catalog](services)
	require.NoError(t, err)
	require.Error(t, catalog.AddTypes(Name, events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}), "event must already be declared")
	require.Error(t, catalog.AddTypes(Name, events.TypeSpec{Name: "result.feedback_added", SchemaVersion: 1, SubjectKind: "result", Realtime: events.RealtimeSignal}), "feedback event must already be declared")
	require.Error(t, catalog.AddTypes(Name, events.TypeSpec{Name: "requirement.scope_changed", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeSignal}), "scope event must already be declared")
	for _, eventType := range []string{"work_item.ready_checked", "work_item.state_changed"} {
		require.Error(t, catalog.AddTypes(Name, events.TypeSpec{Name: eventType, SchemaVersion: 1, SubjectKind: "work_item", Realtime: events.RealtimeSignal}), "readiness event must already be declared: %s", eventType)
	}
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
	sealed := module.NewServices()
	require.ErrorIs(t, (&Module{}).InjectServices(sealed), module.ErrServicesProviding)
	require.NoError(t, sealed.Seal())
	require.ErrorIs(t, (&Module{}).InjectServices(sealed), module.ErrServiceMissing)
	provided := module.NewServices()
	for _, provide := range moduleProviders(t) {
		require.NoError(t, provide(provided))
	}
	require.NoError(t, provided.Seal())
	require.ErrorIs(t, (&Module{}).InjectServices(provided), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).Initialize(t.Context()), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).bindProvisioning(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), ErrUnavailable)
}

func TestModuleRejectsConflictingCapabilityOwnersBeforeOverwriting(t *testing.T) {
	for _, name := range []string{"archive", "budget_history", "acceptance_reminder", "retirement", "confirmation", "controls", "modes", "holds", "clarification", "verifier_policy", "comments", "cost", "quality", "activities", "history", "hidden", "knowledge", "notifications", "workflow_states", "workflow", "verification", "contacts", "client_work", "execution", "planning_templates", "fleet_repository", "code_links"} {
		t.Run(name, func(t *testing.T) {
			m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
			s := module.NewServices()
			providers := map[string]func(*module.Services) error{"archive": m.provideArchiveReviews, "budget_history": m.provideProjectBudgetHistory, "acceptance_reminder": m.provideAcceptanceReminderProjects, "retirement": m.provideReminderWork, "confirmation": m.provideDeliveryConfirmations, "controls": m.provideProjectControls, "modes": m.provideProjectModes, "holds": m.provideExecutionHolds, "clarification": m.provideClarificationReceipts, "verifier_policy": m.provideVerifierWorkPolicy, "comments": m.provideArtifactComments, "cost": m.provideAcceptanceCost, "quality": m.provideReportQuality, "activities": m.provideWorkActivity, "history": m.provideDispatchHistory, "hidden": m.provideHiddenAcceptance, "knowledge": m.provideKnowledgeContext, "notifications": m.provideNotificationFacts, "workflow_states": m.provideWorkflowStates, "workflow": m.provideWorkflowGovernance, "verification": m.provideVerification, "contacts": m.provideAcceptanceContacts, "client_work": m.provideClientWork, "execution": m.provideExecution, "planning_templates": m.providePlanningTemplates, "fleet_repository": m.provideFleetRepository, "code_links": m.provideCodeLinks}
			require.NoError(t, providers[name](s))
			require.ErrorIs(t, m.ProvideServices(s), module.ErrServiceDuplicate)
			require.NoError(t, s.Seal())
		})
	}
}
