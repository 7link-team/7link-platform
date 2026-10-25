package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type testAuth struct{}

func (testAuth) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (testAuth) Check(context.Context, guid.ID, string, guid.ID) (bool, error) { return true, nil }
func (testAuth) CheckPlatform(context.Context, string, guid.ID) (bool, error)  { return false, nil }
func (testAuth) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true}, nil
}
func (testAuth) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) { return true, nil }
func TestModuleInjectsEveryCapabilityAndFailsMissingProviders(t *testing.T) {
	m, _, ctx := configHarness(t)
	require.True(t, m.Enabled())
	require.Equal(t, "yunxiao", m.ModuleName())
	require.Contains(t, m.ModuleDependencies(), delivery.Name)
	require.False(t, NewModule(nil, Config{}).Enabled())
	registrar := func() *authz.Registrar {
		return authz.NewRegistrar(authz.DefaultRegistry(), testAuth{}, testAuth{}, testAuth{})
	}
	makeProviders := func() []func(*module.Services) error {
		return []func(*module.Services) error{
			func(s *module.Services) error {
				return module.Provide[events.EventReader](s, events.EventReader(func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) { return nil, nil }))
			},
			func(s *module.Services) error {
				return module.Provide[intake.ChannelTimingConnector](s, intake.ChannelTimingConnector{Bind: func(reader intake.ChannelTimingReader) error { require.NotNil(t, reader); return nil }})
			},
			func(s *module.Services) error { return module.Provide[intake.APIReceiving](s, intake.APIReceiving{}) },
			func(s *module.Services) error {
				return module.Provide[delivery.WorkflowTransitionConnector](s, delivery.WorkflowTransitionConnector{Bind: func(check delivery.WorkflowTransitionCheck) error { require.NotNil(t, check); return nil }})
			},
			func(s *module.Services) error {
				return module.Provide[delivery.WorkItemVisibilityOn](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil })
			}, func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) }, func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principals) }, func(s *module.Services) error { return module.Provide[core.ActiveMemberChecker](s, m.members) }, func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.policy) }, func(s *module.Services) error { return module.Provide[delivery.ProjectScope](s, m.projects) }, func(s *module.Services) error { return module.Provide[audit.Appender](s, m.audit) }, func(s *module.Services) error { return module.Provide[events.Publisher](s, m.events) }, func(s *module.Services) error { return module.Provide[idempotency.Store](s, m.idem) }, func(s *module.Services) error { return module.Provide[authn.ReauthenticationConsumer](s, m.proof) }, func(s *module.Services) error { return module.Provide[*authz.Registrar](s, registrar()) }, func(s *module.Services) error { return module.Provide[events.Catalog](s, events.NewModule(m.runtime)) }, func(s *module.Services) error {
				return module.Provide[events.SubscriberCatalog](s, events.NewModule(m.runtime))
			}}
	}
	for omit := -1; omit < len(makeProviders()); omit++ {
		services := module.NewServices()
		for i, provide := range makeProviders() {
			if i != omit {
				require.NoError(t, provide(services))
			}
		}
		require.NoError(t, services.Seal())
		candidate := NewModule(m.runtime, m.config)
		e := candidate.InjectServices(services)
		if omit < 0 {
			require.NoError(t, e)
			require.NoError(t, candidate.Initialize(ctx))
		} else {
			require.Error(t, e)
		}
	}
	require.ErrorIs(t, (&Module{}).Initialize(ctx), ErrUnavailable)
	require.Error(t, m.Initialize(ctx), "a namespace cannot be initialized twice")
}
