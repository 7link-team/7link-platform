package idempotency

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModulePublishesTransactionStore(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	require.Equal(t, Name, m.ModuleName())
	require.Equal(t, []string{platformi18n.Name, guid.Name}, m.ModuleDependencies())
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.Error(t, m.InjectServices(missing))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, &testIDs{}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, m.Initialize(t.Context()))
	store, err := module.Resolve[Store](services)
	require.NoError(t, err)
	require.Same(t, m, store)
}
