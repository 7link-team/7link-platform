package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestModuleWiringAndRouteContracts(t *testing.T) {
	original, _, _, _ := harness(t)
	m := NewModule(original.runtime, Config{Config: module.Config{Enabled: true}})
	require.True(t, m.Enabled())
	require.NoError(t, m.Initialize(t.Context()))
	require.Equal(t, Name, m.ModuleName())
	require.NotEmpty(t, m.ModuleDependencies())
	var nilModule *Module
	require.Nil(t, nilModule.writer())
	require.False(t, nilModule.Enabled())
	require.Nil(t, (&Module{}).writer())
	require.Error(t, m.InjectServices(module.NewServices()))
	services := module.NewServices()
	require.NoError(t, module.Provide[execution.PackageEvidenceScanner](services, execution.PackageEvidenceScanner{Scan: execution.ScanPackageEvidence}))
	require.NoError(t, module.Provide[delivery.PackageInputs](services, delivery.PackageInputs{}))
	require.NoError(t, module.Provide[files.DeliveryBundleFiles](services, files.DeliveryBundleFiles{}))
	require.NoError(t, module.Provide[delivery.ClientCommentContacts](services, delivery.ClientCommentContacts{}))
	require.NoError(t, module.Provide[execution.ClientArtifactComments](services, execution.ClientArtifactComments{}))
	require.NoError(t, module.Provide[execution.ClientCommentAuthorityConnector](services, execution.ClientCommentAuthorityConnector{Bind: func(execution.ClientCommentAuthority) error { return nil }}))
	channelDispatchBound, channelFactsBound := false, false
	require.NoError(t, module.Provide[collab.ChannelDispatchConnector](services, collab.ChannelDispatchConnector{
		Bind: func(proposer collab.ChannelDispatchProposer) error {
			channelDispatchBound = proposer != nil
			return nil
		},
		BindFacts: func(facts collab.ChannelDispatchFacts) error {
			channelFactsBound = facts != nil
			return nil
		},
	}))
	require.NoError(t, module.Provide[collab.ChannelPoster](services, collab.ChannelPoster{}))
	require.NoError(t, module.Provide[agents.InboxAppender](services, agents.InboxAppender{}))
	require.NoError(t, module.Provide[fleet.SpawnGrantDirectory](services, fleet.SpawnGrantDirectory{}))
	require.NoError(t, module.Provide[intake.TriageWakeTarget](services, intake.TriageWakeTarget{}))
	require.NoError(t, module.Provide[delivery.ProjectScope](services, delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }}))
	require.NoError(t, module.Provide[intake.TriageAgentConnector](services, intake.TriageAgentConnector{BindAuthority: func(intake.TriageRequestAuthority) error { return nil }}))
	require.NoError(t, module.Provide[identity.ServiceAccountValidator](services, identity.ServiceAccountValidator(func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil })))
	require.NoError(t, module.Provide[core.EntityConstraintOn](services, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})))
	require.NoError(t, module.Provide[execution.HiddenSourceFacts](services, execution.HiddenSourceFacts{}))
	require.NoError(t, module.Provide[delivery.HiddenAcceptanceConnector](services, delivery.HiddenAcceptanceConnector{Bind: func(v delivery.HiddenAcceptanceVerifier) error {
		require.NotNil(t, v.DispatchOn)
		require.NotNil(t, v.PassedOn)
		return nil
	}}))
	require.NoError(t, module.Provide[delivery.VerificationAutomation](services, delivery.VerificationAutomation{}))
	suiteBound := false
	require.NoError(t, module.Provide[delivery.RequirementSuiteConnector](services, delivery.RequirementSuiteConnector{Bind: func(reader delivery.RequirementCaseReader) {
		suiteBound = reader != nil
	}}))
	require.NoError(t, module.Provide[intake.ChangeSources](services, intake.ChangeSources{}))
	require.NoError(t, module.Provide[intake.SubmissionEvents](services, intake.SubmissionEvents{}))
	require.NoError(t, module.Provide[intake.ReworkSources](services, intake.ReworkSources{}))
	require.NoError(t, module.Provide[delivery.PackageSourceFacts](services, delivery.PackageSourceFacts{}))
	require.NoError(t, module.Provide[delivery.IntakeAppender](services, delivery.IntakeAppender{}))
	require.NoError(t, module.Provide[delivery.IntakeDelegatedConversion](services, delivery.IntakeDelegatedConversion{}))
	require.NoError(t, module.Provide[delivery.IntakeProjectCreation](services, delivery.IntakeProjectCreation{}))
	require.NoError(t, module.Provide[delivery.ClientSignerFacts](services, delivery.ClientSignerFacts{}))
	require.NoError(t, module.Provide[delivery.RejectedWorkDisposition](services, delivery.RejectedWorkDisposition{}))
	require.NoError(t, module.Provide[delivery.ClarificationDraftAccess](services, delivery.ClarificationDraftAccess{}))
	bindings, e := files.NewResourceBindings("g_")
	require.NoError(t, e)
	reg, e := authz.NewRegistry(authz.Action{Resource: "requirement", Verb: "read", Scope: "tenant", Summary: "Read requirements"}, authz.Action{Resource: "project", Verb: "read", Scope: "tenant", Summary: "Read project"}, authz.Action{Resource: "scope", Verb: "change", Scope: "tenant", Summary: "Change scope"}, authz.Action{Resource: "channel", Verb: "read", Scope: "tenant", Summary: "Read channels"}, authz.Action{Resource: "project", Verb: "manage", Scope: "tenant", Summary: "Manage project"}, authz.Action{Resource: "delivery", Verb: "send", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Send delivery"})
	require.NoError(t, e)
	registrar := authz.NewDeclarationOnlyRegistrar(reg)
	require.NoError(t, reg.Contribute([]authz.Action{{Resource: "decision", Verb: "request_changes", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "request changes to accepted work"}, {Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, module.Provide[authz.TokenVerifier](services, testVerifier{2}))
	require.NoError(t, module.Provide[files.ClientCollaborationResources](services, original.files))
	require.NoError(t, module.Provide[files.PinnedResources](services, testAnswerFiles{}))
	require.NoError(t, module.Provide[*files.ResourceBindings](services, bindings))
	require.NoError(t, module.Provide[delivery.ClientWorkAccess](services, original.projects))
	require.NoError(t, module.Provide[delivery.DispatchAccess](services, original.dispatch))
	require.NoError(t, module.Provide[delivery.DispatchStarvationFacts](services, delivery.DispatchStarvationFacts{}))
	require.NoError(t, module.Provide[delivery.DispatchScheduleFacts](services, original.dispatchSchedule))
	require.NoError(t, module.Provide[fleet.DispatchFleetFacts](services, original.dispatchFleet))
	require.NoError(t, module.Provide[collab.ClientProjectAccess](services, original.clients))
	require.NoError(t, module.Provide[guid.Source](services, original.ids))
	require.NoError(t, module.Provide[idempotency.Store](services, original.replay))
	require.NoError(t, module.Provide[audit.Appender](services, original.audit))
	require.NoError(t, module.Provide[events.Publisher](services, original.events))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](services, original.principals))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](services, original.members))
	require.NoError(t, module.Provide[*authz.Registrar](services, registrar))
	owner := core.EntityOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	})
	require.NoError(t, module.Provide[core.EntityOwnerResolver](services, owner))
	catalog := events.NewModule(original.runtime)
	catalogServices := module.NewServices()
	require.NoError(t, catalog.ProvideServices(catalogServices))
	require.NoError(t, catalogServices.Seal())
	c, e := module.Resolve[events.Catalog](catalogServices)
	require.NoError(t, e)
	require.NoError(t, module.Provide[events.Catalog](services, c))
	require.NoError(t, module.Provide[events.SubscriberCatalog](services, catalog))
	require.NoError(t, module.Provide[delivery.ForecastWorkFacts](services, delivery.ForecastWorkFacts{}))
	require.NoError(t, module.Provide[execution.ForecastFacts](services, execution.ForecastFacts{}))
	require.NoError(t, module.Provide[budget.SettlementFacts](services, budget.SettlementFacts{}))
	require.NoError(t, module.Provide[jobs.Queue](services, jobs.NewModule(original.runtime, jobs.Config{})))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	require.True(t, channelDispatchBound, "channel dispatch must bind the canonical proposal command")
	require.True(t, channelFactsBound, "channel cards must read canonical proposal facts")
	for _, code := range []string{"test_case_manage", "test_plan_manage", "test_run_execute"} {
		action, found := reg.Find(code)
		require.True(t, found, code)
		require.True(t, action.DataScoped, code)
		require.Equal(t, authz.ActionScopeTenant, action.Scope, code)
	}
	action, found := reg.Find("acceptance_suite_manage")
	require.True(t, found)
	require.True(t, action.DataScoped)
	require.Equal(t, authz.ActionScopeTenant, action.Scope)
	compose, declared := reg.Find("delivery_compose")
	require.True(t, declared, "package domain authorization must resolve a registered action")
	require.True(t, compose.DataScoped)
	require.Equal(t, authz.ActionScopeTenant, compose.Scope)
	require.True(t, suiteBound, "requirement suite reader must be bound during injection")
	require.Equal(t, query.BindNames("g_"), m.names)
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	require.NotNil(t, api.OpenAPI().Paths["/entities/{entity_id}/requirement-sheets"])
	require.NotNil(t, api.OpenAPI().Paths["/entities/{entity_id}/artifact-previews/{id}/content"])
	require.NotNil(t, api.OpenAPI().Paths["/tenants/{tenant_id}/client-access"])
	delegated := api.OpenAPI().Paths["/entities/{entity_id}/intakes/{id}/delegated-requirement"]
	require.NotNil(t, delegated)
	require.Equal(t, "clientwork.intake.delegated_card", delegated.Get.OperationID)
	require.Len(t, delegated.Get.Parameters, 2, "preview accepts only its two path IDs")
	for _, parameter := range delegated.Get.Parameters {
		require.Equal(t, "path", parameter.In)
		require.True(t, parameter.Required)
		require.Contains(t, []string{"entity_id", "id"}, parameter.Name)
	}
	require.Equal(t, "clientwork.intake.delegated_requirement", delegated.Post.OperationID)
	require.NotNil(t, delegated.Post.Responses["201"])
	requested := api.OpenAPI().Paths["/entities/{entity_id}/intakes/{id}/triage-requests"].Post
	require.NotNil(t, requested.Responses["201"])
	require.NotNil(t, requested.Responses["202"])
	require.NotEqual(t, requested.Responses["201"].Description, requested.Responses["202"].Description)
	answer := api.OpenAPI().Paths["/tenants/{tenant_id}/intake-submissions/{id}/clarification-rounds/{round_id}/answers/{question_no}"].Put
	require.NotNil(t, answer.Responses["200"])
	for _, parameter := range answer.Parameters {
		if parameter.Name == "round_id" {
			require.Equal(t, "^[1-9][0-9]*$", parameter.Schema.Pattern)
			require.NotNil(t, parameter.Schema.MaxLength)
			require.Equal(t, 19, *parameter.Schema.MaxLength)
		}
	}
	for _, path := range []string{"/entities/{entity_id}/artifact-previews/{id}/content", "/entities/{entity_id}/delivery-packages/{id}/artifacts/{version_id}/content", "/entities/{entity_id}/preview-shares/{id}/content"} {
		response := api.OpenAPI().Paths[path].Get.Responses["200"]
		require.NotNil(t, response)
		require.NotNil(t, response.Content["*/*"])
		require.Equal(t, "binary", response.Content["*/*"].Schema.Format)
	}

	require.Error(t, (&Module{}).RegisterHTTP(t.Context(), api))
}

func TestModuleCannotStartWithMissingDeclaredDependency(t *testing.T) {
	original, _, _, _ := harness(t)
	bindings, e := files.NewResourceBindings("g_")
	require.NoError(t, e)
	registry, e := authz.NewRegistry(authz.Action{Resource: "channel", Verb: "read", Scope: "tenant", Summary: "Read"})
	require.NoError(t, e)
	registrar := authz.NewDeclarationOnlyRegistrar(registry)
	provide := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[execution.PackageEvidenceScanner](s, execution.PackageEvidenceScanner{Scan: execution.ScanPackageEvidence})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.PackageInputs](s, delivery.PackageInputs{})
		},
		func(s *module.Services) error {
			return module.Provide[files.DeliveryBundleFiles](s, files.DeliveryBundleFiles{})
		},
		func(s *module.Services) error { return module.Provide[*files.ResourceBindings](s, bindings) },
		func(s *module.Services) error {
			return module.Provide[collab.ChannelDispatchConnector](s, collab.ChannelDispatchConnector{
				Bind:      func(collab.ChannelDispatchProposer) error { return nil },
				BindFacts: func(collab.ChannelDispatchFacts) error { return nil },
			})
		},
		func(s *module.Services) error { return module.Provide[collab.ChannelPoster](s, collab.ChannelPoster{}) },
		func(s *module.Services) error {
			return module.Provide[fleet.SpawnGrantDirectory](s, fleet.SpawnGrantDirectory{})
		},
		func(s *module.Services) error { return module.Provide[agents.InboxAppender](s, agents.InboxAppender{}) },
		func(s *module.Services) error {
			return module.Provide[intake.TriageWakeTarget](s, intake.TriageWakeTarget{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.ProjectScope](s, delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }})
		},
		func(s *module.Services) error {
			return module.Provide[intake.TriageAgentConnector](s, intake.TriageAgentConnector{BindAuthority: func(intake.TriageRequestAuthority) error { return nil }})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.RejectedWorkDisposition](s, delivery.RejectedWorkDisposition{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.ClarificationDraftAccess](s, delivery.ClarificationDraftAccess{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.ClientSignerFacts](s, delivery.ClientSignerFacts{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.IntakeProjectCreation](s, delivery.IntakeProjectCreation{})
		},
		func(s *module.Services) error {
			return module.Provide[identity.ServiceAccountValidator](s, identity.ServiceAccountValidator(func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }))
		},
		func(s *module.Services) error {
			return module.Provide[core.EntityConstraintOn](s, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}))
		},
		func(s *module.Services) error {
			return module.Provide[execution.HiddenSourceFacts](s, execution.HiddenSourceFacts{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.HiddenAcceptanceConnector](s, delivery.HiddenAcceptanceConnector{Bind: func(delivery.HiddenAcceptanceVerifier) error { return nil }})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.VerificationAutomation](s, delivery.VerificationAutomation{})
		},
		func(s *module.Services) error {
			return module.Provide[delivery.RequirementSuiteConnector](s, delivery.RequirementSuiteConnector{Bind: func(delivery.RequirementCaseReader) {}})
		},
		func(s *module.Services) error { return module.Provide[delivery.DispatchAccess](s, original.dispatch) },
		func(s *module.Services) error {
			return module.Provide[fleet.DispatchFleetFacts](s, original.dispatchFleet)
		},
		func(s *module.Services) error { return module.Provide[authz.TokenVerifier](s, testVerifier{2}) },
		func(s *module.Services) error {
			return module.Provide[files.ClientCollaborationResources](s, original.files)
		},

		func(s *module.Services) error { return module.Provide[delivery.ClientWorkAccess](s, original.projects) },
		func(s *module.Services) error { return module.Provide[collab.ClientProjectAccess](s, original.clients) },
		func(s *module.Services) error { return module.Provide[guid.Source](s, original.ids) },
		func(s *module.Services) error { return module.Provide[idempotency.Store](s, original.replay) },
		func(s *module.Services) error { return module.Provide[audit.Appender](s, original.audit) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, original.events) },
		func(s *module.Services) error {
			return module.Provide[identity.PrincipalStateStore](s, original.principals)
		},
		func(s *module.Services) error { return module.Provide[core.ActiveMemberChecker](s, original.members) },
		func(s *module.Services) error { return module.Provide[*authz.Registrar](s, registrar) },
		func(s *module.Services) error {
			return module.Provide[core.EntityOwnerResolver](s, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{}, false, nil
			})
		},
	}
	missingNames := []string{"execution.PackageEvidenceScanner", "delivery.PackageInputs", "files.DeliveryBundleFiles", "files.ResourceBindings", "collab.ChannelDispatchConnector", "collab.ChannelPoster", "fleet.SpawnGrantDirectory", "agents.InboxAppender", "intake.TriageWakeTarget", "delivery.ProjectScope", "intake.TriageAgentConnector", "delivery.RejectedWorkDisposition", "delivery.ClarificationDraftAccess", "delivery.ClientSignerFacts", "delivery.IntakeProjectCreation", "identity.ServiceAccountValidator", "core.EntityConstraintOn", "execution.HiddenSourceFacts", "delivery.HiddenAcceptanceConnector", "delivery.VerificationAutomation", "delivery.RequirementSuiteConnector", "delivery.DispatchAccess", "fleet.DispatchFleetFacts", "authz.TokenVerifier", "files.ClientCollaborationResources", "delivery.ClientWorkAccess", "collab.ClientProjectAccess", "guid.Source", "idempotency.Store", "audit.Appender", "events.Publisher", "identity.PrincipalStateStore", "core.ActiveMemberChecker", "authz.Registrar", "core.EntityOwnerResolver"}
	require.Len(t, provide, len(missingNames))
	for missing := 0; missing < len(provide); missing++ {
		bindings, e = files.NewResourceBindings("g_")
		require.NoError(t, e)
		services := module.NewServices()
		require.NoError(t, module.Provide[delivery.ClientCommentContacts](services, delivery.ClientCommentContacts{}))
		require.NoError(t, module.Provide[execution.ClientArtifactComments](services, execution.ClientArtifactComments{}))
		require.NoError(t, module.Provide[execution.ClientCommentAuthorityConnector](services, execution.ClientCommentAuthorityConnector{Bind: func(execution.ClientCommentAuthority) error { return nil }}))
		catalog := events.NewModule(original.runtime)
		require.NoError(t, module.Provide[events.SubscriberCatalog](services, catalog))
		require.NoError(t, module.Provide[intake.ChangeSources](services, intake.ChangeSources{}))
		require.NoError(t, module.Provide[intake.SubmissionEvents](services, intake.SubmissionEvents{}))
		require.NoError(t, module.Provide[intake.ReworkSources](services, intake.ReworkSources{}))
		require.NoError(t, module.Provide[delivery.PackageSourceFacts](services, delivery.PackageSourceFacts{}))
		require.NoError(t, module.Provide[delivery.IntakeAppender](services, delivery.IntakeAppender{}))
		require.NoError(t, module.Provide[delivery.IntakeDelegatedConversion](services, delivery.IntakeDelegatedConversion{}))
		for _, f := range provide[:missing] {
			require.NoError(t, f(services))
		}
		require.NoError(t, services.Seal())
		m := NewModule(original.runtime, Config{})
		err := m.InjectServices(services)
		require.ErrorIs(t, err, module.ErrServiceMissing, "missing dependency %s", missingNames[missing])
		require.Contains(t, err.Error(), missingNames[missing], "each case must reach its intended missing dependency, not fail at an earlier unrelated provider")
	}
}
