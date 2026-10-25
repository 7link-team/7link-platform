package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/notification"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type planningTestCatalog struct{}

func (planningTestCatalog) AddTypes(string, ...events.TypeSpec) error { return nil }

func TestEveryRequiredPlanningCapabilityFailsClosed(t *testing.T) {
	h := setup(t)
	providers := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[clientwork.CollaborationInbox](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]clientwork.CollaborationInboxItem, error) {
				return nil, nil
			})
		},
		func(s *module.Services) error {
			return module.Provide[fleet.WorkbenchMachines](s, h.m.workbenchMachines)
		},
		func(s *module.Services) error {
			return module.Provide[execution.LoopExecutionFacts](s, h.m.loopRuntime.executions)
		},
		func(s *module.Services) error {
			return module.Provide[delivery.LoopAssignments](s, h.m.loopRuntime.assignments)
		},
		func(s *module.Services) error {
			return module.Provide[budget.SettlementFacts](s, h.m.loopRuntime.settlements)
		},
		func(s *module.Services) error {
			return module.Provide[delivery.ProjectBudgets](s, h.m.loopRuntime.projects)
		},
		func(s *module.Services) error { return module.Provide[notification.Notifier](s, h.m.reportNotifier) },
		func(s *module.Services) error { return module.Provide[agents.ReminderLoops](s, h.m.loopReminders) },
		func(s *module.Services) error { return module.Provide[collab.ThreadAnchor](s, h.m.loopAnchors) },
		func(s *module.Services) error { return module.Provide[guid.Source](s, h.m.ids) },
		func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, h.m.principals) },
		func(s *module.Services) error { return module.Provide[core.ActiveMemberChecker](s, h.m.members) },
		func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, h.m.policy) },
		func(s *module.Services) error { return module.Provide[core.EntityConstraintOn](s, h.m.entityPolicy) },
		func(s *module.Services) error { return module.Provide[fleet.AgentDirectory](s, h.m.agents) },
		func(s *module.Services) error { return module.Provide[delivery.ProjectScope](s, h.m.project) },
		func(s *module.Services) error { return module.Provide[delivery.PlanningFacts](s, h.m.facts) },
		func(s *module.Services) error {
			return module.Provide[delivery.ScheduledPlanningFacts](s, h.m.scheduledFacts)
		},
		func(s *module.Services) error { return module.Provide[execution.PeriodReader](s, h.m.executions) },
		func(s *module.Services) error { return module.Provide[audit.Appender](s, h.m.audit) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, h.m.events) },
		func(s *module.Services) error { return module.Provide[idempotency.Store](s, h.m.replay) },
		func(s *module.Services) error {
			registry, e := authz.NewRegistry()
			if e != nil {
				return e
			}
			return module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry))
		},
		func(s *module.Services) error { return module.Provide[governance.InboxSources](s, testInboxSources{}) },
		func(s *module.Services) error { return module.Provide[events.Catalog](s, planningTestCatalog{}) },
	}
	for end := 0; end <= len(providers); end++ {
		s := module.NewServices()
		for _, provide := range providers[:end] {
			require.NoError(t, provide(s))
		}
		require.NoError(t, s.Seal())
		target := NewModule(h.m.runtime, Config{})
		if end < len(providers) {
			require.Error(t, target.InjectServices(s))
		} else {
			require.NoError(t, target.InjectServices(s))
		}
	}
}

func TestModuleLifecycleAndRequiredCapabilities(t *testing.T) {
	h := setup(t)
	require.NoError(t, h.m.Initialize(t.Context()))
	_, api := humatest.New(t)
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	require.Equal(t, Name, h.m.ModuleName())
	require.True(t, h.m.Enabled())
	var nilModule *Module
	require.False(t, nilModule.Enabled())
	require.Contains(t, h.m.ModuleDependencies(), "delivery")
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.Error(t, h.m.InjectServices(s))
}
