package knowledge

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModuleDependenciesAndMissingServices(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	require.Equal(t, Name, m.ModuleName())
	require.True(t, m.Enabled())
	require.NotEmpty(t, m.ModuleDependencies())
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.Error(t, NewModule(m.runtime, Config{}).InjectServices(s))
}

func TestOptionalEmbeddingDoesNotRequireDisabledInfrastructure(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	require.NotContains(t, m.ModuleDependencies(), "secrets")
	m.config.Embedding.Enabled = true
	require.Contains(t, m.ModuleDependencies(), "secrets")
	require.Contains(t, m.ModuleDependencies(), "dlock")
}

func TestKnowledgeStartupRejectsEachMissingCapability(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	for missing := -1; missing < 13; missing++ {
		// Each attempt gets fresh catalogs so duplicate declarations cannot mask a missing service.
		eventServices := module.NewServices()
		require.NoError(t, events.NewModule(m.runtime).ProvideServices(eventServices))
		require.NoError(t, eventServices.Seal())
		catalog, err := module.Resolve[events.Catalog](eventServices)
		require.NoError(t, err)
		subscribers, err := module.Resolve[events.SubscriberCatalog](eventServices)
		require.NoError(t, err)
		registry, err := authz.NewRegistry()
		require.NoError(t, err)
		providers := []func(*module.Services) error{
			func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) },
			func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principals) },
			func(s *module.Services) error { return module.Provide[core.ActiveMemberChecker](s, m.members) },
			func(s *module.Services) error { return module.Provide[core.EntityConstraintOn](s, m.policy) },
			func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.tenantPolicy) },
			func(s *module.Services) error { return module.Provide[fleet.AgentDirectory](s, m.agents) },
			func(s *module.Services) error { return module.Provide[audit.Appender](s, m.audit) },
			func(s *module.Services) error { return module.Provide[events.Publisher](s, m.events) },
			func(s *module.Services) error { return module.Provide[idempotency.Store](s, m.replay) },
			func(s *module.Services) error { return module.Provide[jobs.Queue](s, m.queue) },
			func(s *module.Services) error {
				return module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry))
			},
			func(s *module.Services) error { return module.Provide[events.Catalog](s, catalog) },
			func(s *module.Services) error { return module.Provide[events.SubscriberCatalog](s, subscribers) },
		}
		s := module.NewServices()
		for i, provide := range providers {
			if i != missing {
				require.NoError(t, provide(s))
			}
		}
		require.NoError(t, s.Seal())
		err = NewModule(m.runtime, Config{}).InjectServices(s)
		if missing == -1 {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, module.ErrServiceMissing, "missing capability %d", missing)
		}
	}
}

func TestKnowledgeStartupRejectsIncompleteConfiguredRemoteConnector(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	m.config.Remote = RemoteConfig{Enabled: true, RepositoryHost: "git.example.test"}
	require.ErrorIs(t, m.Initialize(t.Context()), ErrInvalid)
	require.Nil(t, m.remote)
}
