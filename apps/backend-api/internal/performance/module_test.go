package performance

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type catalog func(string, ...events.TypeSpec) error

func (f catalog) AddTypes(owner string, specs ...events.TypeSpec) error { return f(owner, specs...) }
func TestModuleCompositionRequiresEveryCurrentCapability(t *testing.T) {
	m, _, ctx := harness(t)
	require.NoError(t, platformi18n.NewModule(m.runtime, platformi18n.Base).Initialize(ctx))

	registrations := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.policy) },
		func(s *module.Services) error { return module.Provide[identity.PrincipalStateStore](s, m.principals) },
		func(s *module.Services) error { return module.Provide[guid.Source](s, m.ids) },
		func(s *module.Services) error { return module.Provide[audit.Appender](s, m.audit) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, m.events) },
		func(s *module.Services) error { return module.Provide[idempotency.Store](s, m.idem) },
		func(s *module.Services) error {
			registry, e := authz.NewRegistry()
			if e != nil {
				return e
			}
			return module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry))
		},
		func(s *module.Services) error {
			return module.Provide[events.Catalog](s, catalog(func(owner string, specs ...events.TypeSpec) error {
				require.Equal(t, Name, owner)
				require.Equal(t, "performance.scheme_revised", specs[0].Name)
				return nil
			}))
		},
	}
	for n := 0; n <= len(registrations); n++ {
		services := module.NewServices()
		for _, register := range registrations[:n] {
			require.NoError(t, register(services))
		}
		require.NoError(t, services.Seal())
		candidate := NewModule(m.runtime, m.config)
		e := candidate.InjectServices(services)
		if n < len(registrations) {
			require.Error(t, e)
		} else {
			require.NoError(t, e)
			require.NoError(t, candidate.Initialize(ctx))
			require.True(t, candidate.Enabled())
			require.Equal(t, Name, candidate.ModuleName())
			require.Contains(t, candidate.ModuleDependencies(), core.Name)
		}
	}
	require.ErrorIs(t, (&Module{}).Initialize(ctx), ErrUnavailable)
	require.False(t, (&Module{}).Enabled())
	var absent *Module
	require.False(t, absent.Enabled())
}
func TestModuleRejectsUnavailableAndRevokedCurrentIdentities(t *testing.T) {
	m, db, ctx := harness(t)
	require.ErrorIs(t, m.authorizeOn(context.Background(), nil), ErrForbidden)
	require.ErrorIs(t, m.authorizeOn(ctx, nil), ErrUnavailable)
	require.ErrorIs(t, m.authorizeOn(ctx, db), ErrInvalid)
	expected := errors.New("current IAM read failed")
	cases := []func(){
		func() { m.principals.Lock = nil },
		func() { m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return expected } },
		func() {
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, expected
			}
		},
		func() {
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, expected
			}
		},
	}
	originalPrincipals, originalPolicy := m.principals, m.policy
	for _, mutate := range cases {
		m.principals = originalPrincipals
		m.policy = originalPolicy
		mutate()
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeOn(ctx, tx) }))
	}
}
