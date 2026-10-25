package knowledge

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuleNotificationEligibilityRechecksWriterIdentityAndScope(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Evidence rule", Body: "Keep source evidence.", Provenance: "Human proposal"})
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	eligible, err := module.Resolve[RuleNotificationEligible](services)
	require.NoError(t, err)
	check := func(tenant, id, actor guid.ID, expected bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			allowed, err := eligible(t.Context(), tx, tenant, id, actor)
			require.NoError(t, err)
			require.Equal(t, expected, allowed)
			return nil
		}))
	}
	check(1, entry.ID, 3, true)
	for _, actor := range []guid.ID{2, 7, 8, 9} {
		// Writer, Agent, inactive principal and inactive member cannot receive.
		check(1, entry.ID, actor, false)
	}
	check(2, entry.ID, 3, false)
	check(1, entry.ID+1, 3, false)
	m.policy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, guid.ID(3), actor)
		require.Equal(t, "memory_approve", permission)
		return authz.DataConstraint{ResourceIDs: []string{"4"}}, nil
	}
	check(1, entry.ID, 3, false)
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{ResourceIDs: []string{"3"}}, nil
	}
	check(1, entry.ID, 3, true)
	denied := errors.New("current policy unavailable")
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, denied
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := eligible(t.Context(), tx, 1, entry.ID, 3)
		return err
	}), denied)
	_, err = eligible(t.Context(), db, 1, entry.ID, 3)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestRuleNotificationEligibilityReusesAgentWorkspaceAndTeamPolicy(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "RULE", Title: "Agent rule", Body: "Keep verified sources.", Provenance: "Human proposal"})
	workspace := false
	m.tenantPolicy = core.TenantConstraintOn(func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission != "agent_workspace_read" || workspace}, nil
	})
	check := func(id guid.ID, expected bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			allowed, err := m.ruleNotificationEligible(t.Context(), tx, 1, id, 3)
			require.NoError(t, err)
			require.Equal(t, expected, allowed)
			return nil
		}))
	}
	check(entry.ID, false)
	workspace = true
	check(entry.ID, true)
	workspace = false
	check(entry.ID, false)
	directory := m.agents.ByPrincipalOn
	m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentIdentity, bool, error) {
		identity, exists, err := directory(ctx, tx, tenant, agent)
		identity.OwnerPrincipalID = 3
		return identity, exists, err
	}
	// Current ownership is sufficient without workspace-wide permission.
	check(entry.ID, true)
	m.agents.ByPrincipalOn = directory
	check(entry.ID, false)
	team := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Team rule", Body: "Verify all sources.", Provenance: "Human proposal"})
	check(team.ID, true)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	check(team.ID, false)
}

func TestKnowledgePublicationRejectsEveryCompetingCapabilityOwner(t *testing.T) {
	base, _, _ := knowledgeHarness(t)
	providers := []func(*Module, *module.Services) error{
		(*Module).provideRetirementRules, (*Module).provideArchiveResources, (*Module).provideDistillation,
		(*Module).provideMemoryPolicy, (*Module).provideExecutionView, (*Module).provideInjection,
		(*Module).provideEventIngestion, (*Module).provideMemoryEvidence, (*Module).provideSkills,
		(*Module).provideDiagnostics, (*Module).provideReportExperiences, (*Module).provideRuntimeContext,
		(*Module).providePendingRules, (*Module).provideUsage, (*Module).provideLearning,
	}
	for i, provide := range providers {
		m := NewModule(base.runtime, Config{})
		services := module.NewServices()
		require.NoError(t, provide(m, services))
		require.ErrorIs(t, m.ProvideServices(services), module.ErrServiceDuplicate, "competing capability %d", i)
	}
	services := module.NewServices()
	require.NoError(t, module.Provide[RuleNotificationEligible](services, base.ruleNotificationEligible))
	require.ErrorIs(t, NewModule(base.runtime, Config{}).ProvideServices(services), module.ErrServiceDuplicate)
}
