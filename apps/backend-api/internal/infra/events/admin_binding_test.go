package events

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEventsAdminBindingFailsClosedAndPreservesInfraOnlyStartup(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	s := module.NewServices()
	require.NoError(t, m.provideAdmin(s))
	require.Error(t, m.provideAdmin(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AdminConnector](s)
	require.NoError(t, err)
	r := authz.NewUnboundRegistrar(authz.MustRegistry())
	check := func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return true, nil }
	require.ErrorIs(t, c.Bind(nil, check), ErrUnavailable)
	require.ErrorIs(t, c.Bind(r, nil), ErrUnavailable)
	require.ErrorIs(t, c.BindAudit(nil), ErrUnavailable)
	require.ErrorIs(t, c.BindSecurity(nil, nil), ErrUnavailable)
	proof := authn.ReauthenticationConsumer(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil })
	store := idempotency.NewModule(m.runtime)
	require.ErrorIs(t, c.BindSecurity(proof, nil), ErrUnavailable)
	require.ErrorIs(t, c.BindSecurity(nil, store), ErrUnavailable)
	require.NoError(t, c.BindSecurity(proof, store))
	require.NoError(t, c.Bind(r, check))
	require.ErrorIs(t, m.adminAuditOn(t.Context(), nil, "test", 0, "event", 1, nil), ErrUnavailable)
	require.ErrorIs(t, m.adminTransaction(t.Context(), false, func(*gorm.DB) error { return nil }), ErrUnavailable)
	require.ErrorIs(t, (&Module{}).adminAuthorizeOn(t.Context(), nil, false), ErrUnavailable)
	require.ErrorIs(t, m.adminAuthorizeOn(t.Context(), nil, false), errAdminForbidden)
	_, api := humatest.New(t)
	require.NoError(t, (&Module{}).RegisterHTTP(t.Context(), api))
	require.ErrorIs(t, (*Module)(nil).RegisterHTTP(t.Context(), api), ErrUnavailable)
}
