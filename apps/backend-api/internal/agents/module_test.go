package agents

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func copyAgentService[T any](t *testing.T, source, target *module.Services) {
	t.Helper()
	v, e := module.Resolve[T](source)
	require.NoError(t, e)
	require.NoError(t, module.Provide[T](target, v))
}

func TestMessageProjectionPreservesChannelAndThreadStreamReferences(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	thread := guid.ID(4)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, r := range []collab.MessageReference{{TenantID: 1, AgentPrincipalID: 7, MessageID: 11, ChannelID: 3, Seq: 1, Kind: "MESSAGE"}, {TenantID: 1, AgentPrincipalID: 7, MessageID: 12, ChannelID: 3, ThreadID: &thread, Seq: 2, Kind: "MENTION"}} {
			require.NoError(t, m.observeMessageOn(ctx, tx, r))
		}
		page, e := m.inboxOn(ctx, tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Len(t, page.Items, 2)
		require.Equal(t, "channel:3", page.Items[0].StreamKey)
		require.Equal(t, "thread:4", page.Items[1].StreamKey)
		require.Equal(t, "MENTION", page.Items[1].Kind)
		return nil
	}))
	m.events = badPublisher{}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.observeMessageOn(context.Background(), tx, collab.MessageReference{TenantID: 1, AgentPrincipalID: 7, MessageID: 13, ChannelID: 3, Seq: 3, Kind: "MESSAGE"})
	}))
}
func TestModuleCapabilitiesAndDependencyFailures(t *testing.T) {
	m, _, services := agentHarness(t)
	require.Equal(t, "agents", m.ModuleName())
	require.True(t, m.Enabled())
	require.Contains(t, m.ModuleDependencies(), fleet.Name)
	var absent *Module
	require.False(t, absent.Enabled())
	provided := module.NewServices()
	require.NoError(t, m.ProvideServices(provided))
	require.NoError(t, provided.Seal())
	inbox, e := module.Resolve[InboxAppender](provided)
	require.NoError(t, e)
	require.NotNil(t, inbox.AppendOn)
	updates, e := module.Resolve[RuntimeUpdates](provided)
	require.NoError(t, e)
	require.NotNil(t, updates.ReportOn)
	require.NotNil(t, updates.CompleteResetOn)
	registry, e := module.Resolve[*OperationRegistry](provided)
	require.NoError(t, e)
	require.Same(t, m.registry, registry)
	providers := []func(*module.Services){
		func(s *module.Services) { copyAgentService[delivery.SelectedAgentWork](t, services, s) },
		func(s *module.Services) { copyAgentService[collab.AgentDirectReader](t, services, s) },
		func(s *module.Services) { copyAgentService[authn.ReauthenticationConsumer](t, services, s) },
		func(s *module.Services) { copyAgentService[fleet.AgentRuntimeFacts](t, services, s) },
		func(s *module.Services) { copyAgentService[collab.ThreadAccess](t, services, s) },
		func(s *module.Services) { copyAgentService[collab.MessageObserverConnector](t, services, s) },
		func(s *module.Services) { copyAgentService[guid.Source](t, services, s) },
		func(s *module.Services) { copyAgentService[identity.PrincipalStateStore](t, services, s) },
		func(s *module.Services) { copyAgentService[core.ActiveMemberChecker](t, services, s) },
		func(s *module.Services) { copyAgentService[core.EntityConstraintOn](t, services, s) },
		func(s *module.Services) { copyAgentService[core.TenantConstraintOn](t, services, s) },
		func(s *module.Services) { copyAgentService[fleet.AgentDirectory](t, services, s) },
		func(s *module.Services) { copyAgentService[audit.Appender](t, services, s) },
		func(s *module.Services) { copyAgentService[events.Publisher](t, services, s) },
		func(s *module.Services) { copyAgentService[idempotency.Store](t, services, s) },
		func(s *module.Services) { copyAgentService[*authz.Registrar](t, services, s) },
	}
	for missing := range providers {
		s := module.NewServices()
		for i, provide := range providers {
			if i != missing {
				provide(s)
			}
		}
		require.NoError(t, s.Seal())
		candidate := NewModule(m.runtime, m.config)
		require.Error(t, candidate.InjectServices(s))
	}
	complete := module.NewServices()
	for i, provide := range providers {
		if i != len(providers)-1 {
			provide(complete)
		}
	}
	require.NoError(t, module.Provide[events.Catalog](complete, events.NewModule(m.runtime)))
	freshRegistry, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, module.Provide(complete, authz.NewDeclarationOnlyRegistrar(freshRegistry)))
	require.NoError(t, module.Provide(complete, fleet.AgentCatalogPolicy{AllowedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return true, nil }}))
	require.NoError(t, module.Provide(complete, fleet.AgentWorkspaceMachine{BindingOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentWorkspaceFence, error) {
		return fleet.AgentWorkspaceFence{MachineID: 44}, nil
	}}))
	require.NoError(t, module.Provide(complete, core.TeamRoster{}))
	require.NoError(t, module.Provide(complete, identity.TeamRosterPrincipals{}))
	require.NoError(t, module.Provide(complete, fleet.AgentRetirementRuntime{}))
	require.NoError(t, module.Provide(complete, delivery.RetirementWork{}))
	require.NoError(t, module.Provide(complete, knowledge.RetirementRules{}))
	require.NoError(t, module.Provide(complete, delivery.ReminderWorkAccess{}))
	require.NoError(t, complete.Seal())
	wired := NewModule(m.runtime, m.config)
	require.NoError(t, wired.InjectServices(complete))
	allowed, err := wired.catalogPolicy.AllowedOn(t.Context(), nil, 1, 7, "tool")
	require.NoError(t, err)
	require.True(t, allowed)
	fence, err := wired.workspaceMachine.BindingOn(t.Context(), nil, 1, 7)
	require.NoError(t, err)
	require.EqualValues(t, 44, fence.MachineID)
	// Every service slot rejects replacement, rather than silently overwriting.
	for _, provide := range []func(*module.Services){
		func(s *module.Services) { require.NoError(t, module.Provide(s, AgentPerformanceConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, RetirementExecutionConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, RetirementHandoverConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, AgentRosterAnalyticsConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ToolRelayExecutionConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ManifestPublisher{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ReminderCalendarConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, WakeNotifications{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, SkillCatalog{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ContextConnector{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ToolAuthNotifications{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ActivityNotifications{})) },
		func(s *module.Services) { require.NoError(t, module.Provide(s, ReminderLoops{})) },
		func(s *module.Services) { require.NoError(t, module.Provide[*OperationRegistry](s, m.registry)) },
		func(s *module.Services) { require.NoError(t, module.Provide[InboxAppender](s, InboxAppender{})) },
		func(s *module.Services) {
			require.NoError(t, module.Provide[RuntimeControlConnector](s, RuntimeControlConnector{}))
		},
		func(s *module.Services) {
			require.NoError(t, module.Provide[CredentialProofConnector](s, CredentialProofConnector{}))
		},
		func(s *module.Services) { require.NoError(t, module.Provide[RuntimeUpdates](s, RuntimeUpdates{})) },
	} {
		s := module.NewServices()
		provide(s)
		require.Error(t, m.ProvideServices(s))
	}
}
