package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type testCatalog struct{ fail bool }

func (c testCatalog) AddTypes(string, ...events.TypeSpec) error {
	if c.fail {
		return errors.New("catalog failed")
	}
	return nil
}
func TestModuleLifecycleDependenciesAndMissingCapabilities(t *testing.T) {
	m, _, ctx := templateHarness(t)
	m.escalate = func(context.Context, *gorm.DB, governance.EscalationFact) (guid.ID, error) { return 1, nil }
	m.evalFiles = &evalFileFixture{}
	var evalErr error
	m.evalBindings, evalErr = files.NewResourceBindings("t_")
	require.NoError(t, evalErr)
	require.Equal(t, "templates", m.ModuleName())
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	require.Contains(t, m.ModuleDependencies(), delivery.Name)
	require.NotContains(t, m.ModuleDependencies(), secrets.Name, "provider credentials fail explicitly when the optional sealer is disabled")
	require.NoError(t, platformi18n.NewModule(m.runtime, platformi18n.Base).Initialize(ctx))
	require.NoError(t, m.Initialize(ctx))
	require.NotNil(t, m.calibrationEvaluator, "production initialization installs the real protocol evaluator")
	missing := *m
	missing.ids = nil
	require.ErrorIs(t, missing.Initialize(ctx), ErrUnavailable)
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	_ = registry
	providers := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[delivery.RubricSubjects](s, delivery.RubricSubjects{})
		},
		func(s *module.Services) error {
			return module.Provide[core.EntityOwnerResolver](s, core.EntityOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{}, false, nil
			}))
		},

		func(s *module.Services) error { return module.Provide[delivery.TeamWorkOn](s, m.teamWork) },
		func(s *module.Services) error { return module.Provide[delivery.TeamHumanOn](s, m.teamHuman) },
		func(s *module.Services) error {
			return module.Provide[secrets.Sealer](s, secrets.NewModule(secrets.Config{}))
		},
		func(s *module.Services) error { return module.Provide[files.PinnedResources](s, m.evalFiles) },
		func(s *module.Services) error { return module.Provide[*files.ResourceBindings](s, m.evalBindings) },
		func(s *module.Services) error {
			return module.Provide[governance.EscapeEvalConnector](s, governance.EscapeEvalConnector{Bind: func(governance.EscapeEvalWriter) error { return nil }})
		},
		func(s *module.Services) error { return module.Provide[governance.EscalationRecorder](s, m.escalate) },
		func(s *module.Services) error { return module.Provide[intake.SignedBriefs](s, m.briefs) },
		func(s *module.Services) error { return module.Provide[governance.WorkflowDecisions](s, m.gates) },
		func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) },
		func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.policy) },
		func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principals) },
		func(s *module.Services) error { return module.Provide[delivery.ProjectScope](s, m.scope) },
		func(s *module.Services) error { return module.Provide[delivery.TemplateInstantiation](s, m.delivery) },
		func(s *module.Services) error { return module.Provide[audit.Appender](s, m.audit) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, m.events) },
		func(s *module.Services) error { return module.Provide[idempotency.Store](s, m.idem) },
		func(s *module.Services) error {
			r, e := authz.NewRegistry()
			if e != nil {
				return e
			}
			return module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(r))
		},
		func(s *module.Services) error { return module.Provide[events.Catalog](s, testCatalog{}) },
	}
	for end := 0; end < len(providers); end++ {
		s := module.NewServices()
		for _, provide := range providers[:end] {
			require.NoError(t, provide(s))
		}
		require.NoError(t, s.Seal())
		target := NewModule(m.runtime, Config{})
		require.Error(t, target.InjectServices(s))
	}
	s := module.NewServices()
	for _, provide := range providers {
		require.NoError(t, provide(s))
	}
	require.NoError(t, s.Seal())
	target := NewModule(m.runtime, Config{})
	require.NoError(t, target.InjectServices(s))
	failing := module.NewServices()
	for _, provide := range providers[:len(providers)-1] {
		require.NoError(t, provide(failing))
	}
	require.NoError(t, module.Provide[events.Catalog](failing, testCatalog{fail: true}))
	require.NoError(t, failing.Seal())
	require.Error(t, target.InjectServices(failing))
}
